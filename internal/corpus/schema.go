package corpus

import "database/sql"

func Init(db *sql.DB) error {
	stmts := []string{
		`pragma foreign_keys=on`,
		`create table if not exists schema_migrations(version integer primary key, applied_at text default current_timestamp)`,
		`insert or ignore into schema_migrations(version) values(1)`,
		`create table if not exists bundles(bundle_id text primary key,bundle_sha256 text unique,machine_id text,captured_at text,ingested_at text,manifest_json text)`,
		`create table if not exists ingest_attempts(attempt_id integer primary key,bundle_id text,bundle_sha256 text,ingested_at text,duplicate integer)`,
		`create table if not exists machines(machine_id text primary key,first_seen_at text,last_seen_at text,labels_json text)`,
		`create table if not exists sources(source_id integer primary key,source_name text unique,adapter_version text,capabilities_json text)`,
		`create table if not exists files(file_sha256 text primary key,kind text,bytes integer,compressed_blob_path text,first_seen_bundle_id text)`,
		`create table if not exists sessions(session_key text primary key,source_name text,source_session_id text,machine_id text,raw_cwd text,project_key text,started_at text,source_metadata_json text,is_subagent integer default 0,parent_session_key text)`,
		`create table if not exists session_versions(session_key text,file_sha256 text,bundle_id text,relative_path text,raw_path text,observed_at text,copy_state text,unique(session_key,file_sha256,bundle_id))`,
		`create table if not exists entries(session_key text,entry_id text,parent_id text,line_no integer,entry_type text,timestamp text,role text,entry_sha256 text,raw_json text,source_metadata_json text,primary key(session_key,entry_id))`,
		`create table if not exists messages(session_key text,entry_id text,role text,text text,tool_name text,command text,files_json text,model text,provider text,tokens integer,cost real,primary key(session_key,entry_id))`,
		`create table if not exists artifacts(artifact_id integer primary key,artifact_sha256 text,source_name text,machine_id text,bundle_id text,kind text,parent_session_key text,parent_entry_id text,raw_path text,relative_path text,text_preview text,text_body text,unique(artifact_sha256,bundle_id,relative_path,parent_session_key))`,
		`create table if not exists images(image_sha256 text primary key,source_name text,mime_type text,bytes integer,width integer,height integer,ext text,blob_path text)`,
		`create table if not exists entry_assets(session_key text,entry_id text,asset_sha256 text,asset_kind text,content_index integer,prompt_order integer,raw_ref text,mime_type text,metadata_json text,primary key(session_key,entry_id,asset_sha256,content_index,prompt_order))`,
		`create table if not exists conflicts(conflict_id integer primary key,session_key text,entry_id text,first_entry_sha256 text,second_entry_sha256 text,details_json text,created_at text default current_timestamp)`,
		`create virtual table if not exists fts_messages using fts5(session_key unindexed,entry_id unindexed,text)`,
		`create virtual table if not exists fts_artifacts using fts5(artifact_id unindexed,text)`,
		`create index if not exists idx_sessions_source_machine on sessions(source_name,machine_id)`,
		`create index if not exists idx_entries_session_line on entries(session_key,line_no)`,
		`create index if not exists idx_entries_time_role on entries(timestamp,role)`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return err
		}
	}
	return migrate(db)
}

func migrate(db *sql.DB) error {
	hasTextBody, err := columnExists(db, "artifacts", "text_body")
	if err != nil {
		return err
	}
	if !hasTextBody {
		if _, err := db.Exec(`alter table artifacts add column text_body text`); err != nil {
			return err
		}
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
