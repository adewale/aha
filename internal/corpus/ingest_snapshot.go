package corpus

import (
	"io"
	"path/filepath"
	"time"

	"github.com/adewale/aha/internal/cas"
	"github.com/adewale/aha/internal/model"
)

// BlobOpen provides the uncompressed, verified content of one blob a
// snapshot manifest names — typically depot.V2.OpenBlob.
type BlobOpen func(key model.BlobKey) (io.ReadCloser, error)

// IngestSnapshot merges one depot v2 snapshot into the corpus by
// anti-entropy, not replay (docs/depot-v2-spec.md, Phase 5):
//
//   - a snapshot identity already in the corpus is zero work;
//   - a session-file version the corpus proves present (invariant I7) is
//     recorded as provenance without fetching or parsing anything;
//   - content already in the local blob store is read locally;
//   - only genuinely unknown blobs are fetched from the depot.
func (ing Ingestor) IngestSnapshot(manifest model.SnapshotManifest, open BlobOpen) (IngestReport, error) {
	const maxBusyRetries = 20
	for attempt := 0; ; attempt++ {
		if err := ing.context().Err(); err != nil {
			return IngestReport{}, err
		}
		rep, err := ing.ingestSnapshotOnce(manifest, open)
		if !isSQLiteBusy(err) || attempt >= maxBusyRetries {
			return rep, err
		}
		ing.sleeper().Sleep(ing.backoff().Delay(attempt))
	}
}

func (ing Ingestor) ingestSnapshotOnce(manifest model.SnapshotManifest, open BlobOpen) (IngestReport, error) {
	if err := ing.context().Err(); err != nil {
		return IngestReport{}, err
	}
	canonical, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		return IngestReport{}, err
	}
	if err := validateIngestAdapters(manifest.Files, ing.Registry); err != nil {
		return IngestReport{}, err
	}
	tx, err := ing.Store.DB.Begin()
	if err != nil {
		return IngestReport{}, err
	}
	defer tx.Rollback()
	ingestedAt := ing.clock().Now().Format(time.RFC3339)
	dup, skip, err := recordIngestAttempt(tx, sha.String(), ingestedAt)
	if err != nil {
		return IngestReport{}, err
	}
	if skip {
		if err := ing.context().Err(); err != nil {
			return IngestReport{}, err
		}
		if err := tx.Commit(); err != nil {
			return IngestReport{}, err
		}
		return IngestReport{Duplicate: dup}, nil
	}
	if err := insertSnapshotMetadata(tx, sha.String(), manifest.MachineID, manifest.CapturedAt, ingestedAt, string(canonical)); err != nil {
		return IngestReport{}, err
	}
	level := ing.RedactionLevel
	if level == "" {
		level = "none-v1"
	}
	writer := corpusWriter{Context: ing.context(), Store: ing.Store, Registry: ing.Registry, tx: tx, manifest: bundleViewOf(manifest), manifestSHA: sha.String(), redactor: ing.Redactor, redactionLevel: level}
	if err := writer.PrepareStatements(); err != nil {
		return IngestReport{}, err
	}
	defer writer.CloseStatements()
	if err := writer.InsertMachineAndSources(); err != nil {
		return IngestReport{}, err
	}
	rep := IngestReport{Duplicate: dup}
	for _, mf := range manifest.Files {
		if err := ing.context().Err(); err != nil {
			return IngestReport{}, err
		}
		if mf.Kind == "session" {
			knownKey, err := writer.knownSessionVersionKey(mf)
			if err != nil {
				return IngestReport{}, err
			}
			if knownKey != "" {
				if _, err := writer.stmts.insertSessionVersion.Exec(knownKey, mf.SHA256, sha.String(), mf.RelativePath, mf.RawPath, manifest.CapturedAt, mf.CopyState); err != nil {
					return IngestReport{}, err
				}
				rep.Sessions++
				continue
			}
		}
		key, err := model.NewBlobKey(mf.SHA256)
		if err != nil {
			return IngestReport{}, err
		}
		rc, err := ing.openBlobLocalFirst(key, open)
		if err != nil {
			return IngestReport{}, err
		}
		fileRep, ferr := writer.IngestManifestFile(mf, rc)
		if cerr := rc.Close(); ferr == nil {
			ferr = cerr
		}
		if ferr != nil {
			return IngestReport{}, ferr
		}
		rep.Sessions += fileRep.Sessions
		rep.Entries += fileRep.Entries
		rep.Messages += fileRep.Messages
		rep.Images += fileRep.Images
		rep.Artifacts += fileRep.Artifacts
	}
	if err := ing.context().Err(); err != nil {
		return IngestReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return IngestReport{}, err
	}
	return rep, nil
}

// openBlobLocalFirst serves blob content from the corpus's own blob store
// when present (no depot operation), falling back to the depot opener.
func (ing Ingestor) openBlobLocalFirst(key model.BlobKey, open BlobOpen) (io.ReadCloser, error) {
	local, err := cas.Open(filepath.Join(ing.Store.Root, "blobs", "files"))
	if err == nil {
		if ok, _ := local.Has(key); ok {
			if rc, err := local.Open(key); err == nil {
				return rc, nil
			}
		}
	}
	return open(key)
}

// bundleViewOf adapts a snapshot manifest to the writer's source-context
// shape (machine, policy, adapters). Identity fields are not part of the
// view: provenance is keyed by manifestSHA.
func bundleViewOf(m model.SnapshotManifest) model.Manifest {
	return model.Manifest{MachineID: m.MachineID, MachineLabel: m.MachineLabel, CapturedAt: m.CapturedAt, Source: m.Source, Policy: m.Policy, Adapters: m.Adapters, Files: m.Files}
}
