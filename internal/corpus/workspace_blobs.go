package corpus

import (
	"errors"
	"os"

	"github.com/adewale/aha/internal/model"
)

// KnownWorkspaceBlobs inspects durable file identities without creating a
// Workspace lock or SQLite sidecar. An absent Workspace has an empty set.
func KnownWorkspaceBlobs(root string) (map[string]bool, error) {
	store, err := OpenExistingReadOnly(root)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer store.Close()
	rows, err := store.DB.Query(`select file_sha256 from files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		if key, err := model.NewBlobKey(sha); err == nil {
			out[key.String()] = true
		}
	}
	return out, rows.Err()
}
