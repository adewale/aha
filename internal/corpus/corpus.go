package corpus

import (
	"database/sql"
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
	if opts.Migrate {
		if err := Init(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{DB: db, Root: root}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
