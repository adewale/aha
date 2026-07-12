package corpus

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/adewale/aha/internal/model"
)

var ErrArchiveMismatch = errors.New("workspace is bound to a different Archive")

func BindWorkspace(db *sql.DB, binding model.ArchiveBinding) error {
	if !binding.Valid() {
		return errors.New("invalid Archive binding")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var identity, address string
	err = tx.QueryRow(`select archive_identity,archive_address from workspace_binding where singleton=1`).Scan(&identity, &address)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`insert into workspace_binding(singleton,archive_identity,archive_address,schema_version) values(1,?,?,1)`, binding.Identity(), binding.Address()); err != nil {
			return err
		}
	case err != nil:
		return err
	case identity != binding.Identity() || address != binding.Address():
		return ErrArchiveMismatch
	}
	return tx.Commit()
}

func WorkspaceBinding(db *sql.DB) (model.ArchiveBinding, bool, error) {
	var identity, address string
	err := db.QueryRow(`select archive_identity,archive_address from workspace_binding where singleton=1`).Scan(&identity, &address)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ArchiveBinding{}, false, nil
	}
	if err != nil {
		return model.ArchiveBinding{}, false, err
	}
	binding, err := model.NewArchiveBinding(identity, address)
	return binding, err == nil, err
}

func RecordMaterialisedVector(db *sql.DB, vector map[string]string) error {
	machines := make([]string, 0, len(vector))
	for machine, sha := range vector {
		if machine == "" {
			return errors.New("materialised vector contains an empty machine id")
		}
		if _, err := model.NewManifestSHA256(sha); err != nil {
			return fmt.Errorf("materialised vector for machine %q: %w", machine, err)
		}
		machines = append(machines, machine)
	}
	sort.Strings(machines)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`delete from workspace_materialised`); err != nil {
		return err
	}
	for _, machine := range machines {
		if _, err := tx.Exec(`insert into workspace_materialised(machine_id,manifest_sha256) values(?,?)`, machine, vector[machine]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func MaterialisedVector(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`select machine_id,manifest_sha256 from workspace_materialised order by machine_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var machine, sha string
		if err := rows.Scan(&machine, &sha); err != nil {
			return nil, err
		}
		out[machine] = sha
	}
	return out, rows.Err()
}

func WorkspaceState(db *sql.DB, requested model.ArchiveBinding, latest map[string]string) (model.WorkspaceState, error) {
	bound, ok, err := WorkspaceBinding(db)
	if err != nil {
		return model.WorkspaceDamaged, err
	}
	if !ok {
		return model.WorkspaceAbsent, nil
	}
	if bound != requested {
		return model.WorkspaceArchiveMismatch, nil
	}
	materialised, err := MaterialisedVector(db)
	if err != nil {
		return model.WorkspaceDamaged, err
	}
	if equalVector(materialised, latest) {
		return model.WorkspaceCurrent, nil
	}
	return model.WorkspaceBehind, nil
}

func equalVector(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for machine, sha := range a {
		if b[machine] != sha {
			return false
		}
	}
	return true
}
