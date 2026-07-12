package corpus

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

// InspectWorkspaceState is side-effect free: it never creates the directory,
// database, WAL, or lifecycle lock. It is safe to call during preflight.
func InspectWorkspaceState(root string, binding model.ArchiveBinding, latest map[string]string) (model.WorkspaceState, error) {
	expanded, err := paths.Expand(root)
	if err != nil {
		return model.WorkspaceInvalidDestination, err
	}
	dbPath := filepath.Join(expanded, "corpus.db")
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return model.WorkspaceAbsent, nil
	} else if err != nil {
		return model.WorkspaceInvalidDestination, err
	}
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return model.WorkspaceDamaged, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return model.WorkspaceDamaged, fmt.Errorf("open Workspace read-only: %w", err)
	}
	return WorkspaceState(db, binding, latest)
}
