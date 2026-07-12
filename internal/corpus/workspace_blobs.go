package corpus

import (
	"errors"
	"os"

	"github.com/adewale/aha/internal/model"
)

// WorkspaceBlobLookup provides indexed point lookups without loading every
// historical file identity into memory. An absent Workspace behaves as empty.
type WorkspaceBlobLookup struct {
	store *Store
}

func OpenWorkspaceBlobLookup(root string) (*WorkspaceBlobLookup, error) {
	store, err := OpenExistingReadOnly(root)
	if errors.Is(err, os.ErrNotExist) {
		return &WorkspaceBlobLookup{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &WorkspaceBlobLookup{store: store}, nil
}

func (l *WorkspaceBlobLookup) Has(key model.BlobKey) (bool, error) {
	if l == nil || !key.Valid() {
		return false, errors.New("invalid Workspace blob lookup")
	}
	if l.store == nil {
		return false, nil
	}
	var exists bool
	if err := l.store.DB.QueryRow(`select exists(select 1 from files where file_sha256=?)`, key.String()).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (l *WorkspaceBlobLookup) MaterialisedVector() (map[string]string, error) {
	if l == nil {
		return nil, errors.New("invalid Workspace blob lookup")
	}
	if l.store == nil {
		return map[string]string{}, nil
	}
	return MaterialisedVector(l.store.DB)
}

func (l *WorkspaceBlobLookup) Close() error {
	if l == nil || l.store == nil {
		return nil
	}
	return l.store.Close()
}
