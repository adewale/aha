package corpus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/adewale/aha/internal/safety"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB         *sql.DB
	Root       string
	lock       *lifecycleLock
	rootAnchor io.Closer
}

type OpenOptions struct {
	Create  bool
	Migrate bool
}

type WorkspaceReadOnlyBusyError struct{}

func (*WorkspaceReadOnlyBusyError) Error() string {
	return "Workspace has pending WAL state that requires a writable recovery command"
}

type UnsupportedWorkspaceSchemaError struct {
	Found     int
	Supported int
}

func (e *UnsupportedWorkspaceSchemaError) Error() string {
	return fmt.Sprintf("Workspace database schema %d is not supported (this aha supports %d); upgrade aha before using this Workspace", e.Found, e.Supported)
}

func Open(dir string) (*Store, error) {
	return OpenWithOptions(dir, OpenOptions{Create: true, Migrate: true})
}

func OpenExisting(dir string) (*Store, error) {
	return OpenWithOptions(dir, OpenOptions{Create: false, Migrate: true})
}

// OpenExistingReadOnly opens a Workspace without creating directories,
// lifecycle locks, WAL files, or migrations. Inspection commands use it to
// preserve their zero-mutation contract.
func sqliteURIPath(path string) string {
	slashPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	const hex = "0123456789ABCDEF"
	var encoded strings.Builder
	for i := 0; i < len(slashPath); i++ {
		c := slashPath[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("/-._~:", rune(c)) {
			encoded.WriteByte(c)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hex[c>>4])
		encoded.WriteByte(hex[c&15])
	}
	return "file:" + encoded.String()
}

func readOnlySQLiteURI(path string) string {
	// WAL-bearing Workspaces are rejected before this point. immutable makes
	// inspection incapable of creating journals or shared-memory sidecars.
	return sqliteURIPath(path) + "?mode=ro&immutable=1"
}

func OpenExistingReadOnly(dir string) (*Store, error) {
	root, err := paths.Expand(dir)
	if err != nil {
		return nil, err
	}
	root, err = canonicalCorpusIdentity(root)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(root, model.WorkspaceDatabaseFilename)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		return nil, &WorkspaceReadOnlyBusyError{}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := sql.Open("sqlite", readOnlySQLiteURI(dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := rejectNewerWorkspaceSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db, Root: root}, nil
}

func OpenWithOptions(dir string, opts OpenOptions) (*Store, error) {
	return openWithRoots(dir, "", nil, opts)
}

// OpenPreparedDestination is the sole production creating/migrating entry
// point for a safety-checked Workspace destination. It consumes the opaque
// destination proof immediately before opening SQLite.
func OpenPreparedDestination(destination safety.WorkspaceDestination) (*Store, error) {
	root, err := destination.Claim()
	if err != nil {
		return nil, err
	}
	return openAnchored(root.IdentityPath(), root.StoragePath(), root)
}

func openAnchored(identityRoot, storageRoot string, rootAnchor io.Closer) (*Store, error) {
	if rootAnchor == nil || strings.TrimSpace(storageRoot) == "" {
		return nil, errors.New("invalid anchored Workspace root")
	}
	return openWithRoots(identityRoot, storageRoot, rootAnchor, OpenOptions{Create: true, Migrate: true})
}

func openWithRoots(dir, storageRoot string, rootAnchor io.Closer, opts OpenOptions) (*Store, error) {
	ownedAnchor := rootAnchor
	defer func() {
		if ownedAnchor != nil {
			_ = ownedAnchor.Close()
		}
	}()
	root, err := paths.Expand(dir)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root, err = paths.Expand("~/.aha")
		if err != nil {
			return nil, err
		}
	}
	root, err = canonicalCorpusIdentity(root)
	if err != nil {
		return nil, err
	}
	if storageRoot == "" {
		storageRoot = root
	}
	lock, err := acquireLifecycleLock(context.Background(), root, false)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		_ = lock.release()
		return nil, err
	}
	if opts.Create {
		if err := os.MkdirAll(storageRoot, 0o755); err != nil {
			return fail(err)
		}
	} else {
		if st, err := os.Stat(storageRoot); err != nil {
			return fail(err)
		} else if !st.IsDir() {
			return fail(&os.PathError{Op: "open", Path: root, Err: os.ErrInvalid})
		}
		if _, err := os.Stat(filepath.Join(storageRoot, model.WorkspaceDatabaseFilename)); err != nil {
			return fail(err)
		}
	}
	mode := "rw"
	if opts.Create {
		mode = "rwc"
	}
	dbPath := filepath.Join(storageRoot, model.WorkspaceDatabaseFilename)
	db, err := sql.Open("sqlite", sqliteURIPath(dbPath)+"?_txlock=immediate&mode="+mode)
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	if err := rejectNewerWorkspaceSchema(db); err != nil {
		_ = db.Close()
		return fail(err)
	}
	if err := rejectLegacyBundleCorpus(db, storageRoot); err != nil {
		_ = db.Close()
		return fail(err)
	}
	if opts.Migrate {
		if err := Init(db); err != nil {
			_ = db.Close()
			return fail(err)
		}
		if err := refreshWorkspaceIdentitySchema(storageRoot); err != nil {
			_ = db.Close()
			return fail(err)
		}
	}
	store := &Store{DB: db, Root: storageRoot, lock: lock, rootAnchor: rootAnchor}
	ownedAnchor = nil
	return store, nil
}

func rejectNewerWorkspaceSchema(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and name='schema_migrations'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	var version int
	if err := db.QueryRow(`select coalesce(max(version),0) from schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > CurrentWorkspaceSchemaVersion {
		return &UnsupportedWorkspaceSchemaError{Found: version, Supported: CurrentWorkspaceSchemaVersion}
	}
	return nil
}

// rejectLegacyBundleCorpus refuses corpora created before depot v2 (the
// bundle-keyed schema). There is no migration by decision
// (docs/depot-v2-spec.md): a corpus is a rebuildable index over the
// depot, so the legacy schema is rejected at open instead of being
// silently mixed with the snapshot-keyed schema.
func rejectLegacyBundleCorpus(db *sql.DB, root string) error {
	// Two independent signals so a manually-pruned legacy corpus cannot
	// slip through and become a hybrid schema: the bundles table itself,
	// and any surviving table still keyed by bundle_id.
	var n int
	if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and (name='bundles' or (sql like '%bundle_id%' and name<>'bundles'))`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: Workspace at %s uses the pre-0.2 schema; run `aha workspace repair --backup`", ErrLegacyCorpus, root)
	}
	return nil
}

func (s *Store) Close() error {
	dbErr := s.DB.Close()
	var anchorErr error
	if s.rootAnchor != nil {
		anchorErr = s.rootAnchor.Close()
	}
	lockErr := s.lock.release()
	return errors.Join(dbErr, anchorErr, lockErr)
}
