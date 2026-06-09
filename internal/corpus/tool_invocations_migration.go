package corpus

import (
	"database/sql"
	"fmt"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/model"
)

const toolInvocationsSchemaSQL = `create table if not exists tool_invocations(session_key text,entry_id text,tool_key text not null default '',tool_use_id text not null default '',ordinal integer not null default 0,tool_name text,command_family text,command text,exit_code integer,is_error integer default 0,error_signature text,outcome_text text,timestamp text,project_key text,machine_id text,primary key(session_key,entry_id,tool_key,ordinal),foreign key(session_key,entry_id) references entries(session_key,entry_id))`

func migrateToolInvocations(db *sql.DB) error {
	if err := ensureToolInvocationsSchema(db); err != nil {
		return err
	}
	return backfillToolInvocations(db)
}

func ensureToolInvocationsSchema(db *sql.DB) error {
	exists, err := tableExists(db, "tool_invocations")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := db.Exec(toolInvocationsSchemaSQL); err != nil {
			return err
		}
		return ensureToolInvocationAux(db)
	}
	toolKey, err := columnExists(db, "tool_invocations", "tool_key")
	if err != nil {
		return err
	}
	ordinal, err := columnExists(db, "tool_invocations", "ordinal")
	if err != nil {
		return err
	}
	if toolKey && ordinal {
		return ensureToolInvocationAux(db)
	}
	if _, err := db.Exec(`alter table tool_invocations rename to tool_invocations_legacy`); err != nil {
		return err
	}
	if _, err := db.Exec(toolInvocationsSchemaSQL); err != nil {
		return err
	}
	if _, err := db.Exec(`insert or ignore into tool_invocations(session_key,entry_id,tool_key,tool_use_id,ordinal,tool_name,command_family,command,exit_code,is_error,error_signature,outcome_text,timestamp,project_key,machine_id)
select session_key,entry_id,'', '', 0, tool_name,command_family,command,exit_code,is_error,error_signature,outcome_text,timestamp,project_key,machine_id from tool_invocations_legacy`); err != nil {
		return err
	}
	return ensureToolInvocationAux(db)
}

func ensureToolInvocationAux(db *sql.DB) error {
	stmts := []string{
		`create index if not exists idx_tool_invocations_cluster on tool_invocations(is_error,tool_name,command_family,error_signature)`,
		`drop trigger if exists tool_invocations_require_entry`,
		`drop trigger if exists tool_invocations_no_update`,
		`drop trigger if exists tool_invocations_no_delete`,
		`create trigger tool_invocations_require_entry before insert on tool_invocations when not exists(select 1 from entries where session_key=new.session_key and entry_id=new.entry_id) begin select raise(abort,'tool invocation entry missing'); end`,
		`create trigger tool_invocations_no_update before update on tool_invocations begin select raise(abort,'tool invocations are append-only'); end`,
		`create trigger tool_invocations_no_delete before delete on tool_invocations begin select raise(abort,'tool invocations are append-only'); end`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

func backfillToolInvocations(db *sql.DB) error {
	rows, err := db.Query(`select session_key,coalesce(project_key,''),machine_id from sessions order by session_key`)
	if err != nil {
		return err
	}
	type sessionRec struct{ sessionKey, projectKey, machineID string }
	var sessions []sessionRec
	for rows.Next() {
		var r sessionRec
		if err := rows.Scan(&r.sessionKey, &r.projectKey, &r.machineID); err != nil {
			_ = rows.Close()
			return err
		}
		sessions = append(sessions, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range sessions {
		if err := backfillToolInvocationsForSession(db, s.sessionKey, s.projectKey, s.machineID); err != nil {
			return err
		}
	}
	return nil
}

func backfillToolInvocationsForSession(db *sql.DB, sessionKey, projectKey, machineID string) error {
	rows, err := db.Query(`select entry_id,line_no,timestamp,raw_json from entries where session_key=? order by line_no,entry_id`, sessionKey)
	if err != nil {
		return err
	}
	var entries []model.ParsedEntry
	for rows.Next() {
		var entryID, timestamp, rawJSON string
		var lineNo int
		if err := rows.Scan(&entryID, &lineNo, &timestamp, &rawJSON); err != nil {
			_ = rows.Close()
			return err
		}
		calls, results, err := adapters.ExtractToolSignals(rawJSON)
		if err != nil || (len(calls) == 0 && len(results) == 0) {
			continue
		}
		entries = append(entries, model.ParsedEntry{EntryID: entryID, LineNo: lineNo, Timestamp: timestamp, ToolCalls: calls, ToolResults: results})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`insert or ignore into tool_invocations(session_key,entry_id,tool_key,tool_use_id,ordinal,tool_name,command_family,command,exit_code,is_error,error_signature,outcome_text,timestamp,project_key,machine_id) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, inv := range BuildToolInvocations(entries, projectKey, machineID) {
		if !inv.OutcomeObserved {
			continue
		}
		var exitCode any
		if inv.ExitCodeValid {
			exitCode = inv.ExitCode
		}
		sig, outcome := inv.ErrorSignature, inv.ErrorSignature
		// Migration cannot know the original bundle's index_tool_output or extra
		// redaction policy, so fail closed: never derive new cluster display text
		// from historical raw tool output during backfill.
		if inv.IsError {
			sig = fallbackErrorSignature(inv)
			outcome = sig
		}
		if _, err := stmt.Exec(sessionKey, inv.EntryID, inv.ToolKey, inv.ToolUseID, inv.Ordinal, inv.ToolName, inv.CommandFamily, inv.Command, exitCode, boolInt(inv.IsError), sig, outcome, inv.Timestamp, inv.ProjectKey, inv.MachineID); err != nil {
			return fmt.Errorf("backfill tool invocation %s/%s: %w", sessionKey, inv.EntryID, err)
		}
	}
	return tx.Commit()
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(`select name from sqlite_master where type='table' and name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
