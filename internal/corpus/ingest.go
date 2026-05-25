package corpus

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/fileutil"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/media"
	"github.com/adewale/aha/internal/model"
	"github.com/klauspost/compress/zstd"
)

const MaxArtifactTextIndexBytes int64 = 4 << 20

type IngestReport struct {
	Sessions  int  `json:"sessions"`
	Entries   int  `json:"entries"`
	Messages  int  `json:"messages"`
	Images    int  `json:"images"`
	Artifacts int  `json:"artifacts"`
	Duplicate bool `json:"duplicate"`
}

func recordBundleAttempt(tx *sql.Tx, manifest model.Manifest, bundleSHA, ingestedAt string) (duplicate bool, skip bool, err error) {
	var shaForID string
	err = tx.QueryRow(`select bundle_sha256 from bundles where bundle_id=?`, manifest.BundleID).Scan(&shaForID)
	if err == nil {
		if shaForID != bundleSHA {
			return false, false, fmt.Errorf("bundle_id %s already exists with different sha", manifest.BundleID)
		}
		if _, err := tx.Exec(`insert into ingest_attempts(bundle_id,bundle_sha256,ingested_at,duplicate) values(?,?,?,1)`, manifest.BundleID, bundleSHA, ingestedAt); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	var idForSHA string
	err = tx.QueryRow(`select bundle_id from bundles where bundle_sha256=?`, bundleSHA).Scan(&idForSHA)
	if err == nil {
		if _, err := tx.Exec(`insert into ingest_attempts(bundle_id,bundle_sha256,ingested_at,duplicate) values(?,?,?,1)`, manifest.BundleID, bundleSHA, ingestedAt); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	if _, err := tx.Exec(`insert into ingest_attempts(bundle_id,bundle_sha256,ingested_at,duplicate) values(?,?,?,0)`, manifest.BundleID, bundleSHA, ingestedAt); err != nil {
		return false, false, err
	}
	return false, false, nil
}

type ingestHooks struct {
	afterManifestFiles             func() error
	afterBundlePromoteBeforeCommit func() error
}

type Ingestor struct {
	Store    *Store
	Registry map[string]adapters.SourceAdapter
	Clock    ahaclock.Clock
	Sleeper  ahaclock.Sleeper
	Backoff  ahaclock.Backoff
	hooks    ingestHooks
}

type ingestPlan struct {
	stagingPath string
	bundleSHA   string
	bundleBlob  string
	manifest    model.Manifest
}

type bundlePlanner struct {
	Store *Store
}

type blobPublisher struct{}

type corpusWriter struct {
	Store    *Store
	Registry map[string]adapters.SourceAdapter
	tx       *sql.Tx
	manifest model.Manifest
}

func NewIngestor(store *Store, registry map[string]adapters.SourceAdapter) Ingestor {
	return Ingestor{Store: store, Registry: registry, Clock: ahaclock.RealClock{}, Sleeper: ahaclock.RealSleeper{}, Backoff: ahaclock.LinearBackoff{}}
}

func IngestBundle(store *Store, registry map[string]adapters.SourceAdapter, path string) (IngestReport, error) {
	return NewIngestor(store, registry).IngestBundle(path)
}

func (ing Ingestor) IngestBundle(path string) (IngestReport, error) {
	const maxBusyRetries = 20
	for attempt := 0; ; attempt++ {
		rep, err := ing.ingestBundleOnce(path)
		if !isSQLiteBusy(err) || attempt >= maxBusyRetries {
			return rep, err
		}
		ing.sleeper().Sleep(ing.backoff().Delay(attempt))
	}
}

func (ing Ingestor) ingestBundleOnce(path string) (IngestReport, error) {
	store := ing.Store
	plan, err := (bundlePlanner{Store: store}).Prepare(path)
	if err != nil {
		return IngestReport{}, err
	}
	defer os.Remove(plan.stagingPath)
	if err := validateIngestAdapters(plan.manifest, ing.Registry); err != nil {
		return IngestReport{}, err
	}
	tx, err := store.DB.Begin()
	if err != nil {
		return IngestReport{}, err
	}
	defer tx.Rollback()
	ingestedAt := ing.clock().Now().Format(time.RFC3339)
	dup, skip, err := recordBundleAttempt(tx, plan.manifest, plan.bundleSHA, ingestedAt)
	if err != nil {
		return IngestReport{}, err
	}
	if skip {
		if err := tx.Commit(); err != nil {
			return IngestReport{}, err
		}
		return IngestReport{Duplicate: dup}, nil
	}
	if err := insertBundleMetadata(tx, plan, ingestedAt); err != nil {
		return IngestReport{}, err
	}
	manifest := plan.manifest
	writer := corpusWriter{Store: store, Registry: ing.Registry, tx: tx, manifest: manifest}
	if err := writer.InsertMachineAndSources(); err != nil {
		return IngestReport{}, err
	}
	rep := IngestReport{Duplicate: dup}
	err = archive.StreamManifestFiles(plan.stagingPath, func(mf model.ManifestFile, r io.Reader) error {
		fileRep, err := writer.IngestManifestFile(mf, r)
		if err != nil {
			return err
		}
		rep.Sessions += fileRep.Sessions
		rep.Entries += fileRep.Entries
		rep.Messages += fileRep.Messages
		rep.Images += fileRep.Images
		rep.Artifacts += fileRep.Artifacts
		return nil
	})
	if err != nil {
		return IngestReport{}, err
	}
	if ing.hooks.afterManifestFiles != nil {
		if err := ing.hooks.afterManifestFiles(); err != nil {
			return IngestReport{}, err
		}
	}
	promoted, err := (blobPublisher{}).PromoteBundle(plan)
	if err != nil {
		return IngestReport{}, err
	}
	committed := false
	defer func() {
		if promoted && !committed {
			_ = os.Remove(plan.bundleBlob)
		}
	}()
	if ing.hooks.afterBundlePromoteBeforeCommit != nil {
		if err := ing.hooks.afterBundlePromoteBeforeCommit(); err != nil {
			return IngestReport{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return IngestReport{}, err
	}
	committed = true
	return rep, nil
}

func validateIngestAdapters(manifest model.Manifest, registry map[string]adapters.SourceAdapter) error {
	seen := map[string]bool{}
	for _, mf := range manifest.Files {
		if seen[mf.Source] {
			continue
		}
		seen[mf.Source] = true
		if registry[mf.Source] == nil {
			return fmt.Errorf("unknown source adapter %q", mf.Source)
		}
	}
	return nil
}

func (ing Ingestor) clock() ahaclock.Clock {
	if ing.Clock == nil {
		return ahaclock.RealClock{}
	}
	return ing.Clock
}

func (ing Ingestor) sleeper() ahaclock.Sleeper {
	if ing.Sleeper == nil {
		return ahaclock.RealSleeper{}
	}
	return ing.Sleeper
}

func (ing Ingestor) backoff() ahaclock.Backoff {
	if ing.Backoff == nil {
		return ahaclock.LinearBackoff{}
	}
	return ing.Backoff
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func (p bundlePlanner) Prepare(sourcePath string) (ingestPlan, error) {
	stagingDir := filepath.Join(p.Store.Root, "blobs", "bundles")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return ingestPlan{}, err
	}
	staging, err := os.CreateTemp(stagingDir, ".ingest-*.tar.zst")
	if err != nil {
		return ingestPlan{}, err
	}
	stagingPath := staging.Name()
	if err := staging.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return ingestPlan{}, err
	}
	bundleSHA, err := archive.CopyFileHashed(sourcePath, stagingPath)
	if err != nil {
		_ = os.Remove(stagingPath)
		return ingestPlan{}, err
	}
	manifest, err := archive.ReadManifest(stagingPath)
	if err != nil {
		_ = os.Remove(stagingPath)
		return ingestPlan{}, err
	}
	if err := archive.ValidateManifestBudgets(manifest); err != nil {
		_ = os.Remove(stagingPath)
		return ingestPlan{}, err
	}
	return ingestPlan{stagingPath: stagingPath, bundleSHA: bundleSHA, bundleBlob: filepath.Join(p.Store.Root, "blobs", "bundles", bundleSHA+".tar.zst"), manifest: manifest}, nil
}

func insertBundleMetadata(tx *sql.Tx, plan ingestPlan, ingestedAt string) error {
	manifestJSON, _ := json.Marshal(plan.manifest)
	_, err := tx.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?,?)`, plan.manifest.BundleID, plan.bundleSHA, plan.manifest.MachineID, plan.manifest.CapturedAt, ingestedAt, string(manifestJSON))
	return err
}

func (blobPublisher) PromoteBundle(plan ingestPlan) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(plan.bundleBlob), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(plan.bundleBlob); os.IsNotExist(err) {
		if err := os.Rename(plan.stagingPath, plan.bundleBlob); err != nil {
			return false, err
		}
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func (w corpusWriter) InsertMachineAndSources() error {
	manifest := w.manifest
	if _, err := w.tx.Exec(`insert into machines(machine_id,first_seen_at,last_seen_at,labels_json) values(?,?,?,?) on conflict(machine_id) do update set last_seen_at=excluded.last_seen_at`, manifest.MachineID, manifest.CapturedAt, manifest.CapturedAt, mustJSON(map[string]any{"label": manifest.MachineLabel})); err != nil {
		return err
	}
	for _, ad := range manifest.Adapters {
		if _, err := w.tx.Exec(`insert or replace into sources(source_name,adapter_version,capabilities_json) values(?,?,?)`, ad.Name, ad.Version, mustJSON(ad.Capabilities)); err != nil {
			return err
		}
	}
	return nil
}

func (w corpusWriter) IngestManifestFile(mf model.ManifestFile, r io.Reader) (IngestReport, error) {
	store := w.Store
	manifest := w.manifest
	tx := w.tx
	entryReader := io.Reader(r)
	if !manifest.Policy.IncludeImages && mf.Kind != "session" {
		if isImageManifestFile(mf) {
			sha, n, err := hashAndDiscard(r)
			if err != nil {
				return IngestReport{}, err
			}
			if sha != mf.SHA256 || n != mf.Bytes {
				return IngestReport{}, fmt.Errorf("sha/size mismatch for %s", mf.RelativePath)
			}
			return IngestReport{}, nil
		}
		br := bufio.NewReader(r)
		if media.ReaderLooksImage(br) {
			sha, n, err := hashAndDiscard(br)
			if err != nil {
				return IngestReport{}, err
			}
			if sha != mf.SHA256 || n != mf.Bytes {
				return IngestReport{}, fmt.Errorf("sha/size mismatch for %s", mf.RelativePath)
			}
			return IngestReport{}, nil
		}
		entryReader = br
	}
	tmpPath, sha, n, err := spoolEntry(store.Root, entryReader)
	if err != nil {
		return IngestReport{}, err
	}
	defer os.Remove(tmpPath)
	if sha != mf.SHA256 || n != mf.Bytes {
		return IngestReport{}, fmt.Errorf("sha/size mismatch for %s", mf.RelativePath)
	}
	if err := storeFileBlobFromPath(store.Root, mf.SHA256, tmpPath); err != nil {
		return IngestReport{}, err
	}
	if _, err := tx.Exec(`insert or ignore into files(file_sha256,kind,bytes,compressed_blob_path,first_seen_bundle_id) values(?,?,?,?,?)`, mf.SHA256, mf.Kind, mf.Bytes, filepath.ToSlash(filepath.Join("blobs", "files", mf.SHA256+".zst")), manifest.BundleID); err != nil {
		return IngestReport{}, err
	}
	if mf.Kind != "session" {
		added, err := ingestArtifact(tx, store.Root, manifest, mf, tmpPath)
		if err != nil {
			return IngestReport{}, err
		}
		if added {
			return IngestReport{Artifacts: 1}, nil
		}
		return IngestReport{}, nil
	}
	return w.IngestSessionFile(mf, tmpPath)
}

func (w corpusWriter) IngestSessionFile(mf model.ManifestFile, tmpPath string) (IngestReport, error) {
	manifest := w.manifest
	tx := w.tx
	ad := w.Registry[mf.Source]
	if ad == nil {
		return IngestReport{}, fmt.Errorf("unknown source adapter %q", mf.Source)
	}
	fh, err := os.Open(tmpPath)
	if err != nil {
		return IngestReport{}, err
	}
	ps, err := ad.ParseSession(context.Background(), model.SessionFile{Source: mf.Source, Path: mf.RawPath, RelativePath: strings.TrimPrefix(mf.RelativePath, "sources/"+mf.Source+"/sessions/"), SessionID: mf.SessionID, CWD: mf.CWD, StartedAt: mf.StartedAt, IsSubagent: mf.IsSubagent}, fh)
	_ = fh.Close()
	if err != nil {
		return IngestReport{}, err
	}
	if len(ps.Diagnostics) > 0 {
		if ps.Metadata == nil {
			ps.Metadata = map[string]any{}
		}
		ps.Metadata["diagnostics"] = ps.Diagnostics
	}
	sessionID := firstNonEmpty(ps.SourceSessionID, mf.SessionID, strings.TrimSuffix(filepath.Base(mf.RelativePath), filepath.Ext(mf.RelativePath)))
	sessionKeyValue, err := model.NewSessionKey(mf.Source, manifest.MachineID, sessionID)
	if err != nil {
		return IngestReport{}, err
	}
	sessionKey := sessionKeyValue.String()
	if _, err := tx.Exec(`insert or ignore into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json,is_subagent,parent_session_key) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, mf.Source, sessionID, manifest.MachineID, firstNonEmpty(ps.CWD, mf.CWD), projectKey(firstNonEmpty(ps.CWD, mf.CWD)), firstNonEmpty(ps.StartedAt, mf.StartedAt), mustJSON(ps.Metadata), boolInt(ps.IsSubagent || mf.IsSubagent), nil); err != nil {
		return IngestReport{}, err
	}
	if _, err := tx.Exec(`insert or ignore into session_versions(session_key,file_sha256,bundle_id,relative_path,raw_path,observed_at,copy_state) values(?,?,?,?,?,?,?)`, sessionKey, mf.SHA256, manifest.BundleID, mf.RelativePath, mf.RawPath, manifest.CapturedAt, mf.CopyState); err != nil {
		return IngestReport{}, err
	}
	rep := IngestReport{Sessions: 1}
	for _, pe := range ps.Entries {
		if _, err := model.NewEntryID(pe.EntryID); err != nil {
			return IngestReport{}, err
		}
		r, err := ingestEntry(tx, w.Store.Root, manifest, mf.Source, sessionID, sessionKey, pe)
		if err != nil {
			return IngestReport{}, err
		}
		rep.Entries += r.Entries
		rep.Messages += r.Messages
		rep.Images += r.Images
	}
	return rep, nil
}

type entryReport struct{ Entries, Messages, Images int }

func ingestEntry(tx *sql.Tx, root string, manifest model.Manifest, source, sourceSessionID, sessionKey string, pe model.ParsedEntry) (entryReport, error) {
	eh := hash.SHA256Bytes([]byte(pe.RawJSON))
	var oldHash string
	err := tx.QueryRow(`select entry_sha256 from entries where session_key=? and entry_id=?`, sessionKey, pe.EntryID).Scan(&oldHash)
	if err == nil && oldHash != eh {
		_, err := tx.Exec(`insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) values(?,?,?,?,?)`, sessionKey, pe.EntryID, oldHash, eh, mustJSON(map[string]any{"bundle_id": manifest.BundleID}))
		return entryReport{}, err
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return entryReport{}, err
	}
	conflicted, err := detectCrossMachineConflict(tx, manifest, source, sourceSessionID, sessionKey, pe.EntryID, eh)
	if err != nil {
		return entryReport{}, err
	}
	if conflicted {
		return entryReport{}, nil
	}
	res, err := tx.Exec(`insert or ignore into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`, sessionKey, pe.EntryID, pe.ParentID, pe.LineNo, pe.EntryType, pe.Timestamp, pe.Role, eh, pe.RawJSON, mustJSON(pe.Metadata))
	if err != nil {
		return entryReport{}, err
	}
	rep := entryReport{}
	if n, _ := res.RowsAffected(); n > 0 {
		rep.Entries++
	}
	if shouldIndexText(pe, manifest.Policy.IndexToolOutput) {
		res, err := tx.Exec(`insert or ignore into messages(session_key,entry_id,role,text,tool_name,command,files_json,model,provider,tokens,cost) values(?,?,?,?,?,?,?,?,?,?,?)`, sessionKey, pe.EntryID, pe.Role, pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Model, pe.Provider, pe.Tokens, pe.Cost)
		if err != nil {
			return entryReport{}, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rep.Messages++
		}
	}
	if !manifest.Policy.IncludeImages {
		return rep, nil
	}
	for _, asset := range pe.Assets {
		added, err := ingestAsset(tx, root, source, sessionKey, pe.EntryID, asset)
		if err != nil {
			return entryReport{}, err
		}
		if added {
			rep.Images++
		}
	}
	return rep, nil
}

func shouldIndexText(pe model.ParsedEntry, indexToolOutput bool) bool {
	if strings.TrimSpace(pe.Text) == "" {
		return false
	}
	switch pe.Role {
	case "user", "assistant", "branchSummary", "compactionSummary", "summary":
		return true
	case "toolResult", "bashExecution":
		return indexToolOutput
	default:
		return false
	}
}

func detectCrossMachineConflict(tx *sql.Tx, manifest model.Manifest, source, sourceSessionID, sessionKey, entryID, entryHash string) (bool, error) {
	rows, err := tx.Query(`select e.session_key,e.entry_sha256 from entries e join sessions s on s.session_key=e.session_key where s.source_name=? and s.source_session_id=? and s.machine_id<>? and e.entry_id=?`, source, sourceSessionID, manifest.MachineID, entryID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var otherSession, otherHash string
		if err := rows.Scan(&otherSession, &otherHash); err != nil {
			return false, err
		}
		if otherHash != entryHash {
			_, err := tx.Exec(`insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) values(?,?,?,?,?)`, sessionKey, entryID, otherHash, entryHash, mustJSON(map[string]any{"bundle_id": manifest.BundleID, "other_session_key": otherSession, "kind": "cross-machine"}))
			return true, err
		}
	}
	return false, rows.Err()
}

func ingestAsset(tx *sql.Tx, root, source, sessionKey, entryID string, asset model.ParsedAsset) (bool, error) {
	assetSHA := ""
	added := false
	if len(asset.Data) > 0 {
		assetSHA = hash.SHA256Bytes(asset.Data)
		ext := media.ExtFromMIME(asset.MimeType)
		if ext == "" {
			ext = ".bin"
		}
		blobPath := filepath.ToSlash(filepath.Join("blobs", "images", assetSHA+ext))
		if err := writeImageBlobAtomic(root, blobPath, asset.Data); err != nil {
			return false, err
		}
		res, err := tx.Exec(`insert or ignore into images(image_sha256,source_name,mime_type,bytes,width,height,ext,blob_path) values(?,?,?,?,?,?,?,?)`, assetSHA, source, asset.MimeType, len(asset.Data), asset.Width, asset.Height, ext, blobPath)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added = true
		}
	} else {
		assetSHA = hash.SHA256Bytes([]byte(asset.RawRef))
	}
	_, err := tx.Exec(`insert or ignore into entry_assets(session_key,entry_id,asset_sha256,asset_kind,content_index,prompt_order,raw_ref,mime_type,metadata_json) values(?,?,?,?,?,?,?,?,?)`, sessionKey, entryID, assetSHA, asset.AssetKind, asset.ContentIndex, asset.PromptOrder, asset.RawRef, asset.MimeType, mustJSON(asset.Metadata))
	return added, err
}

func writeImageBlobFromPathAtomic(root, relPath, srcPath string) error {
	finalPath := filepath.Join(root, relPath)
	if _, err := os.Stat(finalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "image-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func writeImageBlobAtomic(root, relPath string, data []byte) error {
	finalPath := filepath.Join(root, relPath)
	if _, err := os.Stat(finalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "image-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func readArtifactText(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxArtifactTextIndexBytes+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(b)) > MaxArtifactTextIndexBytes {
		b = b[:MaxArtifactTextIndexBytes]
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	if !utf8.Valid(b) {
		return "", "", nil
	}
	fullText := string(b)
	preview := fullText
	if len(preview) > 4000 {
		preview = preview[:4000]
	}
	return preview, fullText, nil
}

func ingestArtifact(tx *sql.Tx, root string, manifest model.Manifest, mf model.ManifestFile, path string) (bool, error) {
	preview, fullText, err := readArtifactText(path)
	if err != nil {
		return false, err
	}
	var parent any
	if mf.ParentHint != "" {
		key, err := model.NewSessionKey(mf.Source, manifest.MachineID, mf.ParentHint)
		if err != nil {
			return false, err
		}
		parent = key.String()
	}
	res, err := tx.Exec(`insert or ignore into artifacts(artifact_sha256,source_name,machine_id,bundle_id,kind,parent_session_key,parent_entry_id,raw_path,relative_path,text_preview,text_body) values(?,?,?,?,?,?,?,?,?,?,?)`, mf.SHA256, mf.Source, manifest.MachineID, manifest.BundleID, mf.Kind, parent, nil, mf.RawPath, mf.RelativePath, preview, fullText)
	if err != nil {
		return false, err
	}
	added := false
	if n, _ := res.RowsAffected(); n > 0 {
		added = true
		if manifest.Policy.IncludeImages {
			if err := maybeStoreImageArtifact(tx, root, manifest, mf, path); err != nil {
				return false, err
			}
		}
	}
	return added, nil
}

func isImageManifestFile(mf model.ManifestFile) bool {
	_, ok := media.ImageMIMEFromPath(firstNonEmpty(mf.RawPath, mf.RelativePath))
	if ok {
		return true
	}
	_, ok = media.ImageMIMEFromPath(mf.RelativePath)
	return ok
}

func maybeStoreImageArtifact(tx *sql.Tx, root string, manifest model.Manifest, mf model.ManifestFile, path string) error {
	mt, ok := media.ImageMIMEFromPath(firstNonEmpty(mf.RawPath, mf.RelativePath))
	if !ok {
		mt, ok = media.ImageMIMEFromPath(mf.RelativePath)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var head [16]byte
	n, _ := io.ReadFull(f, head[:])
	if !ok {
		mt, ok = media.ImageMIMEFromBytes(head[:n])
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	if !ok {
		_ = f.Close()
		return nil
	}
	width, height := media.Dimensions(f)
	_ = f.Close()
	ext := filepath.Ext(mf.RawPath)
	if ext == "" {
		ext = media.ExtFromMIME(mt)
	}
	if ext == "" {
		ext = ".bin"
	}
	blobPath := filepath.ToSlash(filepath.Join("blobs", "images", mf.SHA256+ext))
	if err := writeImageBlobFromPathAtomic(root, blobPath, path); err != nil {
		return err
	}
	_, err = tx.Exec(`insert or ignore into images(image_sha256,source_name,mime_type,bytes,width,height,ext,blob_path) values(?,?,?,?,?,?,?,?)`, mf.SHA256, mf.Source, mt, mf.Bytes, width, height, ext, blobPath)
	return err
}

func hashAndDiscard(r io.Reader) (string, int64, error) {
	res, err := fileutil.HashDiscard(r)
	if err != nil {
		return "", 0, err
	}
	return res.SHA256, res.Bytes, nil
}

func spoolEntry(root string, r io.Reader) (string, string, int64, error) {
	dir := filepath.Join(root, "blobs", "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, err
	}
	out, err := os.CreateTemp(dir, "entry-*")
	if err != nil {
		return "", "", 0, err
	}
	path := out.Name()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), r)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", "", 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", "", 0, closeErr
	}
	return path, hex.EncodeToString(h.Sum(nil)), n, nil
}

func storeFileBlobFromPath(root, sha, path string) error {
	dir := filepath.Join(root, "blobs", "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	finalPath := filepath.Join(dir, sha+".zst")
	if _, err := os.Stat(finalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, sha+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := out.Name()
	enc, err := zstd.NewWriter(out)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_, copyErr := io.Copy(enc, in)
	closeEncErr := enc.Close()
	closeOutErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if closeEncErr != nil {
		_ = os.Remove(tmpPath)
		return closeEncErr
	}
	if closeOutErr != nil {
		_ = os.Remove(tmpPath)
		return closeOutErr
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
