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
	witness, witnessed, witnessErr := WorkspaceIdentity(expanded)
	if witnessErr != nil {
		var unsupported *UnsupportedWorkspaceIdentityError
		if errors.As(witnessErr, &unsupported) {
			return model.WorkspaceUpgradeRequired, nil
		}
		return model.WorkspaceDamaged, witnessErr
	}
	if witnessed && witness != binding {
		return model.WorkspaceArchiveMismatch, nil
	}
	dbPath := filepath.Join(expanded, "corpus.db")
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		if witnessed {
			return model.WorkspaceDamaged, nil
		}
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
		if witnessed {
			return model.WorkspaceDamaged, nil
		}
		return model.WorkspaceDamaged, fmt.Errorf("open Workspace read-only: %w", err)
	}
	if err := rejectNewerWorkspaceSchema(db); err != nil {
		var unsupported *UnsupportedWorkspaceSchemaError
		if errors.As(err, &unsupported) {
			return model.WorkspaceUpgradeRequired, nil
		}
		return model.WorkspaceDamaged, err
	}
	state, err := WorkspaceState(db, binding, latest)
	if err != nil && witnessed {
		return model.WorkspaceDamaged, nil
	}
	if witnessed && state == model.WorkspaceArchiveMismatch {
		return model.WorkspaceDamaged, nil
	}
	return state, err
}
