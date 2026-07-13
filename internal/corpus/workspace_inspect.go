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
	dbPath := filepath.Join(expanded, model.WorkspaceDatabaseFilename)
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		if witnessed {
			return model.WorkspaceDamaged, nil
		}
		return model.WorkspaceAbsent, nil
	} else if err != nil {
		return model.WorkspaceInvalidDestination, err
	}
	db, err := sql.Open("sqlite", readOnlySQLiteURI(dbPath))
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
	return InspectOpenWorkspaceState(expanded, db, binding, latest)
}

// InspectOpenWorkspaceState applies binding/vector checks to an already-open
// read-only database. Status uses it to avoid reopening SQLite or running a
// full verification scan; `workspace verify` owns integrity auditing.
func InspectOpenWorkspaceState(root string, db *sql.DB, binding model.ArchiveBinding, latest map[string]string) (model.WorkspaceState, error) {
	witness, witnessed, witnessErr := WorkspaceIdentity(root)
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
	state, err := WorkspaceState(db, binding, latest)
	if err != nil && witnessed {
		return model.WorkspaceDamaged, nil
	}
	if witnessed && state == model.WorkspaceArchiveMismatch {
		return model.WorkspaceDamaged, nil
	}
	return state, err
}
