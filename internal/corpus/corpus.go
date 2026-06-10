package corpus

import (
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
	if opts.Create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	} else {
		if st, err := os.Stat(root); err != nil {
			return nil, err
		} else if !st.IsDir() {
			return nil, &os.PathError{Op: "open", Path: root, Err: os.ErrInvalid}
		}
		if _, err := os.Stat(filepath.Join(root, "corpus.db")); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := rejectLegacyBundleCorpus(db, root); err != nil {
		db.Close()
		return nil, err
	}
	if opts.Migrate {
		if err := Init(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{DB: db, Root: root}, nil
}

// rejectLegacyBundleCorpus refuses corpora created before depot v2 (the
// bundle-keyed schema). There is no migration by decision
// (docs/depot-v2-spec.md): a corpus is a rebuildable index over the
// depot, so the legacy schema is rejected at open instead of being
// silently mixed with the snapshot-keyed schema.
func rejectLegacyBundleCorpus(db *sql.DB, root string) error {
	var n int
	if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and name='bundles'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("corpus at %s uses the pre-v2 bundle schema; rebuild it by re-ingesting from the depot (move the old corpus directory aside first)", root)
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }
