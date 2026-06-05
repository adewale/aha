package corpus

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// failure_episodes is a deterministic projection of tool_invocations: one row
// per single-session failure arc (see docs/outcome-weighting-spec.md). It is
// rebuildable from tool_invocations, which is itself rebuildable from
// entries.raw_json, so it carries no independent truth.
//
// Correctness-by-construction is enforced in the schema: the CHECK ties the
// resolved flag to the presence of resolve_entry_id / resolution_path /
// resolve_ordinal / resolved_at, so an abandoned episode cannot carry a phantom
// fix and a resolved one cannot be missing its exact invocation evidence.
const failureEpisodesSchemaSQL = `create table if not exists failure_episodes(
  session_key text not null,
  open_entry_id text not null,
  open_ordinal integer not null default 0,
  tool_name text,
  command_family text,
  error_signature text,
  resolved integer not null check(resolved in (0,1)),
  resolve_entry_id text,
  resolve_ordinal integer,
  resolution_path text,
  project_key text,
  opened_at text,
  resolved_at text,
  primary key(session_key, open_entry_id, open_ordinal),
  foreign key(session_key, open_entry_id) references entries(session_key, entry_id),
  check(
    (resolved=1 and resolve_entry_id is not null and resolve_ordinal is not null and resolution_path is not null and resolved_at is not null)
    or
    (resolved=0 and resolve_entry_id is null and resolve_ordinal is null and resolution_path is null and resolved_at is null)
  )
)`

// queryer is satisfied by both *sql.DB and *sql.Tx, so episode assembly reads
// the same way during the migration backfill and during an in-transaction
// ingest. (execer, with the same dual purpose, lives in fts_reconcile.go.)
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func migrateFailureEpisodes(db *sql.DB) error {
	if err := ensureFailureEpisodesSchema(db); err != nil {
		return err
	}
	return backfillFailureEpisodes(db)
}

func ensureFailureEpisodesSchema(db *sql.DB) error {
	if _, err := db.Exec(failureEpisodesSchemaSQL); err != nil {
		return err
	}
	hasResolveOrdinal, err := columnExists(db, "failure_episodes", "resolve_ordinal")
	if err != nil {
		return err
	}
	if !hasResolveOrdinal {
		if _, err := db.Exec(`alter table failure_episodes add column resolve_ordinal integer`); err != nil {
			return err
		}
	}
	stmts := []string{
		`create index if not exists idx_failure_episodes_cluster on failure_episodes(tool_name,command_family,error_signature,resolved)`,
		// failure_episodes is a *derived* view of tool_invocations, recomputed
		// per session on ingest (delete + reinsert), so — unlike the immutable
		// tool_invocations rows it projects — it is intentionally NOT
		// append-only: a session resumed after its fix arrives must be able to
		// flip an abandoned episode to resolved. The CHECK constraint, not a
		// no-update trigger, is the real invariant.
		`create trigger if not exists failure_episodes_require_entry before insert on failure_episodes when not exists(select 1 from entries where session_key=new.session_key and entry_id=new.open_entry_id) begin select raise(abort,'failure episode entry missing'); end`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

func migrateFailureEpisodeResolveOrdinals(db *sql.DB) error {
	exists, err := tableExists(db, "failure_episodes")
	if err != nil {
		return err
	}
	if exists {
		// failure_episodes is a derived projection. Recreating it is the safest
		// way to strengthen SQLite CHECK constraints for corpora that already ran
		// the earlier v14 branch migration before resolve_ordinal existed.
		if _, err := db.Exec(`drop table failure_episodes`); err != nil {
			return err
		}
	}
	if err := ensureFailureEpisodesSchema(db); err != nil {
		return err
	}
	return backfillFailureEpisodes(db)
}

func backfillFailureEpisodes(db *sql.DB) error {
	rows, err := db.Query(`select distinct session_key from tool_invocations order by session_key`)
	if err != nil {
		return err
	}
	var sessions []string
	for rows.Next() {
		var sk string
		if err := rows.Scan(&sk); err != nil {
			_ = rows.Close()
			return err
		}
		sessions = append(sessions, sk)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sk := range sessions {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := rebuildFailureEpisodesForSession(tx, sk); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// txLike is satisfied by both *sql.DB and *sql.Tx: the rebuild reads the
// session's tool_invocations and rewrites its failure_episodes through the same
// handle, so it works inside the ingest writer transaction and in the migration
// backfill alike.
type txLike interface {
	queryer
	execer
}

// rebuildFailureEpisodesForSession recomputes one session's episodes as a pure
// function of its stored tool_invocations: delete the existing rows, then
// insert the freshly assembled set. This makes the projection correct on
// resume — a session re-ingested after its resolving success arrives flips its
// abandoned episode to resolved instead of keeping a stale row — and idempotent
// for unchanged sessions.
func rebuildFailureEpisodesForSession(x txLike, sessionKey string) error {
	eps, err := failureEpisodesForSession(x, sessionKey)
	if err != nil {
		return err
	}
	if _, err := x.Exec(`delete from failure_episodes where session_key=?`, sessionKey); err != nil {
		return err
	}
	return insertFailureEpisodes(x, eps)
}

// failureEpisodesForSession reads a session's stored tool_invocations and
// assembles its failure episodes. Reading the stored (already-redacted) rows —
// rather than re-deriving from raw entries — keeps the episode's command
// families and error signatures identical to what clusters display, with no
// new bypass around redaction or index_tool_output.
func failureEpisodesForSession(q queryer, sessionKey string) ([]FailureEpisode, error) {
	rows, err := q.Query(`select ti.entry_id,ti.ordinal,e.line_no,ti.tool_name,ti.command_family,ti.error_signature,ti.is_error,ti.timestamp,ti.project_key
from tool_invocations ti
join entries e on e.session_key=ti.session_key and e.entry_id=ti.entry_id
where ti.session_key=?`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invs []ToolInvocation
	for rows.Next() {
		var (
			iv     ToolInvocation
			isErr  int
			errSig sql.NullString
			proj   sql.NullString
			tname  sql.NullString
			fam    sql.NullString
			ts     sql.NullString
		)
		if err := rows.Scan(&iv.EntryID, &iv.Ordinal, &iv.LineNo, &tname, &fam, &errSig, &isErr, &ts, &proj); err != nil {
			return nil, err
		}
		iv.SessionKey = sessionKey
		iv.ToolName = tname.String
		iv.CommandFamily = fam.String
		iv.ErrorSignature = errSig.String
		iv.IsError = isErr != 0
		iv.Timestamp = ts.String
		iv.ProjectKey = proj.String
		invs = append(invs, iv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return assembleEpisodes(invs, DefaultEpisodeConfig()), nil
}

// insertFailureEpisodes writes episodes append-only (insert or ignore). The
// abandoned zero-state stores SQL NULLs for the resolve_* columns so the
// schema CHECK is satisfied; a resolved episode stores its evidence.
func insertFailureEpisodes(x execer, eps []FailureEpisode) error {
	for _, ep := range eps {
		var resolveEntryID, resolveOrdinal, resolvedAt, pathJSON any
		if ep.Resolved {
			resolveEntryID = ep.ResolveEntryID
			resolveOrdinal = ep.ResolveOrdinal
			resolvedAt = ep.ResolvedAt
			b, err := json.Marshal(ep.ResolutionPath)
			if err != nil {
				return fmt.Errorf("marshal resolution path for %s/%s: %w", ep.SessionKey, ep.OpenEntryID, err)
			}
			pathJSON = string(b)
		}
		if _, err := x.Exec(`insert or ignore into failure_episodes(session_key,open_entry_id,open_ordinal,tool_name,command_family,error_signature,resolved,resolve_entry_id,resolve_ordinal,resolution_path,project_key,opened_at,resolved_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ep.SessionKey, ep.OpenEntryID, ep.OpenOrdinal, ep.ToolName, ep.CommandFamily, ep.ErrorSignature,
			boolInt(ep.Resolved), resolveEntryID, resolveOrdinal, pathJSON, ep.ProjectKey, ep.OpenedAt, resolvedAt); err != nil {
			return fmt.Errorf("insert failure episode %s/%s: %w", ep.SessionKey, ep.OpenEntryID, err)
		}
	}
	return nil
}
