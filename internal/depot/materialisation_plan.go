package depot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/model"
	_ "modernc.org/sqlite"
)

type spooledManifest struct {
	machine string
	sha     model.ManifestSHA256
	path    string
}

// MaterialisationPlan is a disk-spooled, latest-vector-scoped capability.
// Changed manifests are validated before Workspace mutation, but are not
// retained together on the heap. The zero value authorises no blob access.
type MaterialisationPlan struct {
	reader   PreparedPull
	latest   map[string]model.ManifestSHA256
	spooled  []spooledManifest
	summary  DownloadPlanSummary
	tempRoot string
}

func (p DownloadPlan) PrepareMaterialisation(ctx context.Context, materialised map[string]string, supported map[string]string, known func(model.BlobKey) (bool, error)) (*MaterialisationPlan, error) {
	machines, err := p.Machines()
	if err != nil {
		return nil, err
	}
	if known == nil {
		known = func(model.BlobKey) (bool, error) { return false, nil }
	}
	root, err := os.MkdirTemp("", "aha-materialisation-plan-*")
	if err != nil {
		return nil, err
	}
	plan := &MaterialisationPlan{
		reader:   p.reader,
		latest:   cloneLatestVector(p.latest),
		summary:  DownloadPlanSummary{Machines: len(machines), LatestSnapshots: len(p.latest)},
		tempRoot: root,
	}
	fail := func(err error) (*MaterialisationPlan, error) {
		_ = plan.Close()
		return nil, err
	}
	dedup, err := sql.Open("sqlite", filepath.Join(root, "summary.db")+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)")
	if err != nil {
		return fail(err)
	}
	if _, err := dedup.Exec(`create table blobs(sha text primary key) without rowid`); err != nil {
		_ = dedup.Close()
		return fail(err)
	}
	tx, err := dedup.Begin()
	if err != nil {
		_ = dedup.Close()
		return fail(err)
	}
	insertBlob, err := tx.Prepare(`insert or ignore into blobs(sha) values(?)`)
	if err != nil {
		_ = tx.Rollback()
		_ = dedup.Close()
		return fail(err)
	}
	failDedup := func(err error) (*MaterialisationPlan, error) {
		_ = insertBlob.Close()
		_ = tx.Rollback()
		_ = dedup.Close()
		return fail(err)
	}
	for _, machine := range machines {
		sha := p.latest[machine]
		if materialised[machine] == sha.String() {
			continue
		}
		prepared, err := p.selectedPreparedManifest(ctx, machine)
		if err != nil {
			return failDedup(err)
		}
		manifest := prepared.Manifest()
		if err := requireManifestAdapters(manifest, supported); err != nil {
			return failDedup(err)
		}
		if prepared.SHA() != sha {
			return failDedup(fmt.Errorf("selected manifest changed identity during planning"))
		}
		path := filepath.Join(root, fmt.Sprintf("%06d.json", len(plan.spooled)))
		if err := os.WriteFile(path, prepared.Canonical(), 0o600); err != nil {
			return failDedup(err)
		}
		plan.spooled = append(plan.spooled, spooledManifest{machine: machine, sha: sha, path: path})
		for _, file := range manifest.Files {
			insert, err := insertBlob.Exec(file.SHA256)
			if err != nil {
				return failDedup(err)
			}
			inserted, err := insert.RowsAffected()
			if err != nil {
				return failDedup(err)
			}
			if inserted == 0 {
				continue
			}
			key, err := model.NewBlobKey(file.SHA256)
			if err != nil {
				return failDedup(err)
			}
			present, err := known(key)
			if err != nil {
				return failDedup(err)
			}
			if !present {
				plan.summary.UnknownBlobs++
				plan.summary.UnknownBytes += file.Bytes
			}
		}
	}
	if err := insertBlob.Close(); err != nil {
		return failDedup(err)
	}
	if err := tx.Commit(); err != nil {
		_ = dedup.Close()
		return fail(err)
	}
	if err := dedup.Close(); err != nil {
		return fail(err)
	}
	return plan, nil
}

func cloneLatestVector(in map[string]model.ManifestSHA256) map[string]model.ManifestSHA256 {
	out := make(map[string]model.ManifestSHA256, len(in))
	for machine, sha := range in {
		out[machine] = sha
	}
	return out
}

func requireManifestAdapters(manifest model.SnapshotManifest, supported map[string]string) error {
	for _, requirement := range manifest.Adapters {
		version, ok := supported[requirement.Name]
		if !ok {
			return &UnsupportedSnapshotAdapterError{Adapter: requirement.Name, RequiredVersion: requirement.Version}
		}
		if version != requirement.Version {
			return &UnsupportedSnapshotAdapterError{Adapter: requirement.Name, RequiredVersion: requirement.Version, SupportedVersion: version}
		}
	}
	return nil
}

func (p *MaterialisationPlan) Summary() (DownloadPlanSummary, error) {
	if p == nil || p.reader.depot == nil || p.latest == nil || p.tempRoot == "" {
		return DownloadPlanSummary{}, errors.New("invalid Archive materialisation plan")
	}
	return p.summary, nil
}

func (p *MaterialisationPlan) ArchiveBinding() (model.ArchiveBinding, error) {
	if p == nil {
		return model.ArchiveBinding{}, errors.New("invalid Archive materialisation plan")
	}
	return p.reader.ArchiveBinding()
}

func (p *MaterialisationPlan) LatestVector() (map[string]string, error) {
	if p == nil || p.reader.depot == nil || p.latest == nil {
		return nil, errors.New("invalid Archive materialisation plan")
	}
	out := make(map[string]string, len(p.latest))
	for machine, sha := range p.latest {
		out[machine] = sha.String()
	}
	return out, nil
}

// ForEachManifest loads at most one spooled manifest at a time and grants blob
// access only to content addressed by the frozen selected manifests.
func (p *MaterialisationPlan) ForEachManifest(ctx context.Context, apply func(machine string, prepared model.PreparedSnapshot, open func(model.BlobKey) (io.ReadCloser, error)) error) error {
	if p == nil || p.reader.depot == nil || p.tempRoot == "" || apply == nil {
		return errors.New("invalid Archive materialisation plan")
	}
	for _, selected := range p.spooled {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := os.ReadFile(selected.path)
		if err != nil {
			return err
		}
		prepared, err := model.DecodePreparedSnapshot(b)
		if err != nil {
			return err
		}
		manifest := prepared.Manifest()
		if prepared.SHA() != selected.sha || manifest.MachineID != selected.machine {
			return errors.New("spooled materialisation manifest identity changed")
		}
		allowed := make(map[string]bool, len(manifest.Files))
		for _, file := range manifest.Files {
			allowed[file.SHA256] = true
		}
		open := func(key model.BlobKey) (io.ReadCloser, error) {
			if !key.Valid() || !allowed[key.String()] {
				return nil, errors.New("blob is outside the frozen latest vector")
			}
			return p.reader.openBlob(ctx, key)
		}
		if err := apply(selected.machine, prepared, open); err != nil {
			return err
		}
	}
	return nil
}

func (p *MaterialisationPlan) Close() error {
	if p == nil || p.tempRoot == "" {
		return nil
	}
	root := p.tempRoot
	p.tempRoot = ""
	p.spooled = nil
	return os.RemoveAll(root)
}
