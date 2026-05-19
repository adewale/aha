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

func Open(dir string) (*Store, error) {
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
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "corpus.db"))
	if err != nil {
		return nil, err
	}
	if err := Init(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, Root: root}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
