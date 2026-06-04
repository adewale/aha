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
	"sync"
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

var zstdEncoderPool sync.Pool

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
	expectedSHA string
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
	stmts    writerStatements
}

type writerStatements struct {
	insertMachine           *sql.Stmt
	upsertSource            *sql.Stmt
	insertFile              *sql.Stmt
	insertSession           *sql.Stmt
	insertSessionVersion    *sql.Stmt
	insertSessionPathToken  *sql.Stmt
	insertEntry             *sql.Stmt
	insertMessage           *sql.Stmt
	insertConflict          *sql.Stmt
	insertArtifact          *sql.Stmt
	insertArtifactPathToken *sql.Stmt
	insertImage             *sql.Stmt
	insertEntryAsset        *sql.Stmt
}

func (w *corpusWriter) PrepareStatements() error {
	prepared := []struct {
		dst **sql.Stmt
		sql string
	}{
		{&w.stmts.insertMachine, `insert into machines(machine_id,first_seen_at,last_seen_at,labels_json) values(?,?,?,?) on conflict(machine_id) do update set last_seen_at=excluded.last_seen_at`},
		{&w.stmts.upsertSource, `insert or replace into sources(source_name,adapter_version,capabilities_json) values(?,?,?)`},
		{&w.stmts.insertFile, `insert or ignore into files(file_sha256,kind,bytes,compressed_blob_path,first_seen_bundle_id) values(?,?,?,?,?)`},
		{&w.stmts.insertSession, `insert or ignore into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at,source_metadata_json,is_subagent,parent_session_key) values(?,?,?,?,?,?,?,?,?,?)`},
		{&w.stmts.insertSessionVersion, `insert or ignore into session_versions(session_key,file_sha256,bundle_id,relative_path,raw_path,observed_at,copy_state) values(?,?,?,?,?,?,?)`},
		{&w.stmts.insertSessionPathToken, `insert or ignore into session_path_tokens(session_key,token) values(?,?)`},
		{&w.stmts.insertEntry, `insert or ignore into entries(session_key,entry_id,parent_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json,source_metadata_json) values(?,?,?,?,?,?,?,?,?,?)`},
		{&w.stmts.insertMessage, `insert or ignore into messages(session_key,entry_id,role,text,tool_name,command,files_json,model,provider,tokens,cost) values(?,?,?,?,?,?,?,?,?,?,?)`},
		{&w.stmts.insertConflict, `insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) values(?,?,?,?,?)`},
		{&w.stmts.insertArtifact, `insert or ignore into artifacts(artifact_sha256,source_name,machine_id,bundle_id,kind,parent_session_key,parent_entry_id,raw_path,relative_path,text_preview,text_body) values(?,?,?,?,?,?,?,?,?,?,?)`},
		{&w.stmts.insertArtifactPathToken, `insert or ignore into artifact_path_tokens(artifact_id,token) values(?,?)`},
		{&w.stmts.insertImage, `insert or ignore into images(image_sha256,source_name,mime_type,bytes,width,height,ext,blob_path) values(?,?,?,?,?,?,?,?)`},
		{&w.stmts.insertEntryAsset, `insert or ignore into entry_assets(session_key,entry_id,asset_sha256,asset_kind,content_index,prompt_order,raw_ref,mime_type,metadata_json) values(?,?,?,?,?,?,?,?,?)`},
	}
	for _, item := range prepared {
		stmt, err := w.tx.Prepare(item.sql)
		if err != nil {
			return err
		}
		*item.dst = stmt
	}
	return nil
}

func (w *corpusWriter) CloseStatements() error {
	var first error
	for _, stmt := range []*sql.Stmt{w.stmts.insertMachine, w.stmts.upsertSource, w.stmts.insertFile, w.stmts.insertSession, w.stmts.insertSessionVersion, w.stmts.insertSessionPathToken, w.stmts.insertEntry, w.stmts.insertMessage, w.stmts.insertConflict, w.stmts.insertArtifact, w.stmts.insertArtifactPathToken, w.stmts.insertImage, w.stmts.insertEntryAsset} {
		if stmt == nil {
			continue
		}
		if err := stmt.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func NewIngestor(store *Store, registry map[string]adapters.SourceAdapter) Ingestor {
	return Ingestor{Store: store, Registry: registry, Clock: ahaclock.RealClock{}, Sleeper: ahaclock.RealSleeper{}, Backoff: ahaclock.LinearBackoff{}}
}

func IngestBundle(store *Store, registry map[string]adapters.SourceAdapter, path string) (IngestReport, error) {
	return NewIngestor(store, registry).IngestBundle(path)
}

func IngestBundleWithExpectedSHA(store *Store, registry map[string]adapters.SourceAdapter, path, expectedSHA string) (IngestReport, error) {
	return NewIngestor(store, registry).IngestBundleWithExpectedSHA(path, expectedSHA)
}

func (ing Ingestor) IngestBundle(path string) (IngestReport, error) {
	return ing.IngestBundleWithExpectedSHA(path, "")
}

func (ing Ingestor) IngestBundleWithExpectedSHA(path, expectedSHA string) (IngestReport, error) {
	const maxBusyRetries = 20
	for attempt := 0; ; attempt++ {
		rep, err := ing.ingestBundleOnce(path, expectedSHA)
		if !isSQLiteBusy(err) || attempt >= maxBusyRetries {
			return rep, err
		}
		ing.sleeper().Sleep(ing.backoff().Delay(attempt))
	}
}

func (ing Ingestor) ingestBundleOnce(path, expectedSHA string) (IngestReport, error) {
	store := ing.Store
	plan, err := (bundlePlanner{Store: store}).Prepare(path, expectedSHA)
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
	if err := writer.PrepareStatements(); err != nil {
		return IngestReport{}, err
	}
	defer writer.CloseStatements()
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

func (p bundlePlanner) Prepare(sourcePath, expectedSHA string) (ingestPlan, error) {
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
	if expectedSHA != "" && bundleSHA != expectedSHA {
		_ = os.Remove(stagingPath)
		return ingestPlan{}, fmt.Errorf("bundle sha mismatch: expected=%s actual=%s", expectedSHA, bundleSHA)
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
	return ingestPlan{stagingPath: stagingPath, bundleSHA: bundleSHA, bundleBlob: filepath.Join(p.Store.Root, "blobs", "bundles", bundleSHA+".tar.zst"), manifest: manifest, expectedSHA: expectedSHA}, nil
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
	if _, err := w.stmts.insertMachine.Exec(manifest.MachineID, manifest.CapturedAt, manifest.CapturedAt, mustJSON(map[string]any{"label": manifest.MachineLabel})); err != nil {
		return err
	}
	for _, ad := range manifest.Adapters {
		if _, err := w.stmts.upsertSource.Exec(ad.Name, ad.Version, mustJSON(ad.Capabilities)); err != nil {
			return err
		}
	}
	return nil
}

func (w corpusWriter) IngestManifestFile(mf model.ManifestFile, r io.Reader) (IngestReport, error) {
	store := w.Store
	manifest := w.manifest
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
	knownBlob, err := w.fileBlobKnown(mf.SHA256)
	if err != nil {
		return IngestReport{}, err
	}
	if !knownBlob {
		if err := storeFileBlobFromPath(store.Root, mf.SHA256, tmpPath); err != nil {
			return IngestReport{}, err
		}
	}
	if _, err := w.stmts.insertFile.Exec(mf.SHA256, mf.Kind, mf.Bytes, filepath.ToSlash(filepath.Join("blobs", "files", mf.SHA256+".zst")), manifest.BundleID); err != nil {
		return IngestReport{}, err
	}
	if mf.Kind != "session" {
		added, err := w.ingestArtifact(mf, tmpPath)
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

func (w corpusWriter) fileBlobKnown(sha string) (bool, error) {
	var rel string
	err := w.tx.QueryRow(`select compressed_blob_path from files where file_sha256=?`, sha).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if rel == "" {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(w.Store.Root, filepath.FromSlash(rel))); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
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
	rawCWD := firstNonEmpty(ps.CWD, mf.CWD)
	if _, err := w.stmts.insertSession.Exec(sessionKey, mf.Source, sessionID, manifest.MachineID, rawCWD, projectKey(rawCWD), firstNonEmpty(ps.StartedAt, mf.StartedAt), mustJSON(ps.Metadata), boolInt(ps.IsSubagent || mf.IsSubagent), nil); err != nil {
		return IngestReport{}, err
	}
	if err := w.insertSessionPathTokens(sessionKey, rawCWD); err != nil {
		return IngestReport{}, err
	}
	if _, err := w.stmts.insertSessionVersion.Exec(sessionKey, mf.SHA256, manifest.BundleID, mf.RelativePath, mf.RawPath, manifest.CapturedAt, mf.CopyState); err != nil {
		return IngestReport{}, err
	}
	existingEntries, err := existingEntryHashes(tx, sessionKey)
	if err != nil {
		return IngestReport{}, err
	}
	crossConflicts, err := crossMachineEntryHashes(tx, manifest, mf.Source, sessionID)
	if err != nil {
		return IngestReport{}, err
	}
	rep := IngestReport{Sessions: 1}
	for _, pe := range ps.Entries {
		if _, err := model.NewEntryID(pe.EntryID); err != nil {
			return IngestReport{}, err
		}
		r, err := w.ingestEntry(mf.Source, sessionID, sessionKey, pe, existingEntries, crossConflicts)
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

type crossEntryHash struct {
	sessionKey string
	hash       string
}

func existingEntryHashes(tx *sql.Tx, sessionKey string) (map[string]string, error) {
	rows, err := tx.Query(`select entry_id,entry_sha256 from entries where session_key=?`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var entryID, entryHash string
		if err := rows.Scan(&entryID, &entryHash); err != nil {
			return nil, err
		}
		out[entryID] = entryHash
	}
	return out, rows.Err()
}

func crossMachineEntryHashes(tx *sql.Tx, manifest model.Manifest, source, sourceSessionID string) (map[string][]crossEntryHash, error) {
	rows, err := tx.Query(`select e.entry_id,e.session_key,e.entry_sha256 from entries e indexed by idx_entries_session_entry_hash join sessions s indexed by idx_sessions_source_session on s.session_key=e.session_key where s.source_name=? and s.source_session_id=? and s.machine_id<>?`, source, sourceSessionID, manifest.MachineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]crossEntryHash{}
	for rows.Next() {
		var entryID, sessionKey, entryHash string
		if err := rows.Scan(&entryID, &sessionKey, &entryHash); err != nil {
			return nil, err
		}
		out[entryID] = append(out[entryID], crossEntryHash{sessionKey: sessionKey, hash: entryHash})
	}
	return out, rows.Err()
}

func (w corpusWriter) ingestEntry(source, sourceSessionID, sessionKey string, pe model.ParsedEntry, existing map[string]string, cross map[string][]crossEntryHash) (entryReport, error) {
	eh := hash.SHA256Bytes([]byte(pe.RawJSON))
	if oldHash, ok := existing[pe.EntryID]; ok && oldHash != eh {
		_, err := w.stmts.insertConflict.Exec(sessionKey, pe.EntryID, oldHash, eh, mustJSON(map[string]any{"bundle_id": w.manifest.BundleID}))
		return entryReport{}, err
	}
	for _, other := range cross[pe.EntryID] {
		if other.hash != eh {
			_, err := w.stmts.insertConflict.Exec(sessionKey, pe.EntryID, other.hash, eh, mustJSON(map[string]any{"bundle_id": w.manifest.BundleID, "other_session_key": other.sessionKey, "kind": "cross-machine"}))
			return entryReport{}, err
		}
	}
	res, err := w.stmts.insertEntry.Exec(sessionKey, pe.EntryID, pe.ParentID, pe.LineNo, pe.EntryType, pe.Timestamp, pe.Role, eh, pe.RawJSON, mustJSON(pe.Metadata))
	if err != nil {
		return entryReport{}, err
	}
	rep := entryReport{}
	if n, _ := res.RowsAffected(); n > 0 {
		rep.Entries++
		existing[pe.EntryID] = eh
	}
	if shouldPersistMessage(pe, w.manifest.Policy.IndexToolOutput) {
		res, err := w.stmts.insertMessage.Exec(sessionKey, pe.EntryID, pe.Role, pe.Text, pe.ToolName, pe.Command, pe.FilesJSON, pe.Model, pe.Provider, pe.Tokens, pe.Cost)
		if err != nil {
			return entryReport{}, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rep.Messages++
		}
	}
	if !w.manifest.Policy.IncludeImages {
		return rep, nil
	}
	for _, asset := range pe.Assets {
		added, err := w.ingestAsset(source, sessionKey, pe.EntryID, asset)
		if err != nil {
			return entryReport{}, err
		}
		if added {
			rep.Images++
		}
	}
	return rep, nil
}

func (w corpusWriter) insertSessionPathTokens(sessionKey, rawCWD string) error {
	for _, token := range pathTokens(rawCWD) {
		if _, err := w.stmts.insertSessionPathToken.Exec(sessionKey, token); err != nil {
			return err
		}
	}
	return nil
}

func (w corpusWriter) insertArtifactPathTokens(artifactID int64, paths ...string) error {
	for _, token := range pathTokens(paths...) {
		if _, err := w.stmts.insertArtifactPathToken.Exec(artifactID, token); err != nil {
			return err
		}
	}
	return nil
}

func shouldPersistMessage(pe model.ParsedEntry, indexToolOutput bool) bool {
	if shouldIndexText(pe, indexToolOutput) {
		return true
	}
	return strings.TrimSpace(pe.ToolName) != "" || strings.TrimSpace(pe.Command) != "" || strings.TrimSpace(pe.FilesJSON) != "" || strings.TrimSpace(pe.Model) != "" || strings.TrimSpace(pe.Provider) != "" || pe.Tokens != 0 || pe.Cost != 0
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

func (w corpusWriter) ingestAsset(source, sessionKey, entryID string, asset model.ParsedAsset) (bool, error) {
	assetSHA := ""
	added := false
	if len(asset.Data) > 0 {
		assetSHA = hash.SHA256Bytes(asset.Data)
		ext := media.ExtFromMIME(asset.MimeType)
		if ext == "" {
			ext = ".bin"
		}
		blobPath := filepath.ToSlash(filepath.Join("blobs", "images", assetSHA+ext))
		if err := writeImageBlobAtomic(w.Store.Root, blobPath, asset.Data); err != nil {
			return false, err
		}
		res, err := w.stmts.insertImage.Exec(assetSHA, source, asset.MimeType, len(asset.Data), asset.Width, asset.Height, ext, blobPath)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added = true
		}
	} else {
		assetSHA = hash.SHA256Bytes([]byte(asset.RawRef))
	}
	_, err := w.stmts.insertEntryAsset.Exec(sessionKey, entryID, assetSHA, asset.AssetKind, asset.ContentIndex, asset.PromptOrder, asset.RawRef, asset.MimeType, mustJSON(asset.Metadata))
	return added, err
}

func writeImageBlobFromPathAtomic(root, relPath, srcPath string) error {
	return fileutil.AtomicCopyFile(filepath.Join(root, relPath), srcPath, fileutil.AtomicOptions{TempPattern: "image-*.tmp", ExistingOK: true})
}

func writeImageBlobAtomic(root, relPath string, data []byte) error {
	return fileutil.AtomicWriteBytes(filepath.Join(root, relPath), data, fileutil.AtomicOptions{TempPattern: "image-*.tmp", ExistingOK: true})
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

func (w corpusWriter) ingestArtifact(mf model.ManifestFile, path string) (bool, error) {
	manifest := w.manifest
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
	res, err := w.stmts.insertArtifact.Exec(mf.SHA256, mf.Source, manifest.MachineID, manifest.BundleID, mf.Kind, parent, nil, mf.RawPath, mf.RelativePath, preview, fullText)
	if err != nil {
		return false, err
	}
	added := false
	if n, _ := res.RowsAffected(); n > 0 {
		added = true
		artifactID, err := res.LastInsertId()
		if err != nil {
			return false, err
		}
		if err := w.insertArtifactPathTokens(artifactID, mf.RawPath, mf.RelativePath); err != nil {
			return false, err
		}
		if manifest.Policy.IncludeImages {
			if err := w.maybeStoreImageArtifact(mf, path); err != nil {
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

func (w corpusWriter) maybeStoreImageArtifact(mf model.ManifestFile, path string) error {
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
	if err := writeImageBlobFromPathAtomic(w.Store.Root, blobPath, path); err != nil {
		return err
	}
	_, err = w.stmts.insertImage.Exec(mf.SHA256, mf.Source, mt, mf.Bytes, width, height, ext, blobPath)
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
	finalPath := filepath.Join(root, "blobs", "files", sha+".zst")
	return fileutil.AtomicWrite(finalPath, fileutil.AtomicOptions{TempPattern: sha + "-*.tmp", ExistingOK: true}, func(out *os.File) error {
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		enc, err := pooledZstdWriter(out)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(enc, in)
		closeEncErr := enc.Close()
		if closeEncErr == nil {
			putZstdWriter(enc)
		}
		if copyErr != nil {
			return copyErr
		}
		return closeEncErr
	})
}

func pooledZstdWriter(w io.Writer) (*zstd.Encoder, error) {
	if v := zstdEncoderPool.Get(); v != nil {
		enc := v.(*zstd.Encoder)
		enc.Reset(w)
		return enc, nil
	}
	return zstd.NewWriter(w)
}

func putZstdWriter(enc *zstd.Encoder) {
	enc.Reset(io.Discard)
	zstdEncoderPool.Put(enc)
}
