package corpus

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/paths"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	Root string
	lock *lifecycleLock
}

type OpenOptions struct {
	Create  bool
	Migrate bool
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
func OpenExistingReadOnly(dir string) (*Store, error) {
	root, err := paths.Expand(dir)
	if err != nil {
		return nil, err
	}
	root, err = canonicalCorpusIdentity(root)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(root, "corpus.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db, Root: root}, nil
}

func OpenWithOptions(dir string, opts OpenOptions) (*Store, error) {
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
	lock, err := acquireLifecycleLock(context.Background(), root, false)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		_ = lock.release()
		return nil, err
	}
	if opts.Create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fail(err)
		}
	} else {
		if st, err := os.Stat(root); err != nil {
			return fail(err)
		} else if !st.IsDir() {
			return fail(&os.PathError{Op: "open", Path: root, Err: os.ErrInvalid})
		}
		if _, err := os.Stat(filepath.Join(root, "corpus.db")); err != nil {
			return fail(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	if err := rejectLegacyBundleCorpus(db, root); err != nil {
		_ = db.Close()
		return fail(err)
	}
	if opts.Migrate {
		if err := Init(db); err != nil {
			_ = db.Close()
			return fail(err)
		}
	}
	return &Store{DB: db, Root: root, lock: lock}, nil
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
	lockErr := s.lock.release()
	if dbErr != nil {
		return dbErr
	}
	return lockErr
}
