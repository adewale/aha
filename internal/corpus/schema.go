package corpus

import "database/sql"

func Init(db *sql.DB) error {
	stmts := []string{
		`pragma foreign_keys=on`,
		`pragma busy_timeout=5000`,
		`pragma journal_mode=wal`,
		`create table if not exists schema_migrations(version integer primary key, applied_at text default current_timestamp)`,
		`insert or ignore into schema_migrations(version) values(1)`,
		`create table if not exists bundles(bundle_id text primary key check(bundle_id<>''),bundle_sha256 text unique,machine_id text,captured_at text,ingested_at text,manifest_json text)`,
		`create table if not exists ingest_attempts(attempt_id integer primary key,bundle_id text,bundle_sha256 text,ingested_at text,duplicate integer)`,
		`create table if not exists machines(machine_id text primary key check(machine_id<>''),first_seen_at text,last_seen_at text,labels_json text)`,
		`create table if not exists sources(source_id integer primary key,source_name text unique check(source_name<>''),adapter_version text,capabilities_json text)`,
		`create table if not exists files(file_sha256 text primary key check(length(file_sha256)=64),kind text,bytes integer check(bytes>=0),compressed_blob_path text,first_seen_bundle_id text references bundles(bundle_id))`,
		`create table if not exists sessions(session_key text primary key check(length(session_key)=68 and substr(session_key,1,4)='sk1_' and substr(session_key,5) not glob '*[^0-9a-f]*'),source_name text not null check(source_name<>''),source_session_id text not null check(source_session_id<>''),machine_id text not null check(machine_id<>''),raw_cwd text,project_key text,started_at text,source_metadata_json text,is_subagent integer default 0,parent_session_key text references sessions(session_key))`,
		`create table if not exists session_versions(session_key text references sessions(session_key),file_sha256 text references files(file_sha256),bundle_id text references bundles(bundle_id),relative_path text,raw_path text,observed_at text,copy_state text,unique(session_key,file_sha256,bundle_id))`,
		`create table if not exists entries(session_key text,entry_id text,parent_id text,line_no integer,entry_type text,timestamp text,role text,entry_sha256 text,raw_json text,source_metadata_json text,primary key(session_key,entry_id),check(session_key<>'' and entry_id<>''),foreign key(session_key) references sessions(session_key))`,
		`create table if not exists messages(session_key text,entry_id text,role text,text text,tool_name text,command text,files_json text,model text,provider text,tokens integer,cost real,primary key(session_key,entry_id),foreign key(session_key,entry_id) references entries(session_key,entry_id))`,
		`create table if not exists artifacts(artifact_id integer primary key,artifact_sha256 text check(length(artifact_sha256)=64),source_name text,machine_id text,bundle_id text not null references bundles(bundle_id),kind text,parent_session_key text,parent_entry_id text,raw_path text,relative_path text,text_preview text,text_body text,unique(artifact_sha256,bundle_id,relative_path,parent_session_key))`,
		`create table if not exists images(image_sha256 text primary key check(length(image_sha256)=64),source_name text,mime_type text,bytes integer check(bytes>=0),width integer,height integer,ext text,blob_path text)`,
		`create table if not exists entry_assets(session_key text,entry_id text,asset_sha256 text,asset_kind text,content_index integer,prompt_order integer,raw_ref text,mime_type text,metadata_json text,primary key(session_key,entry_id,asset_sha256,content_index,prompt_order),foreign key(session_key,entry_id) references entries(session_key,entry_id))`,
		`create table if not exists conflicts(conflict_id integer primary key,session_key text,entry_id text,first_entry_sha256 text,second_entry_sha256 text,details_json text,created_at text default current_timestamp)`,
		`create virtual table if not exists fts_messages using fts5(session_key unindexed,entry_id unindexed,text)`,
		`create virtual table if not exists fts_artifacts using fts5(artifact_id unindexed,text)`,
		`create index if not exists idx_sessions_source_machine on sessions(source_name,machine_id)`,
		`create index if not exists idx_sessions_source_session on sessions(source_name,source_session_id,machine_id)`,
		`create trigger if not exists sessions_require_v2_key before insert on sessions when new.session_key='' or length(new.session_key)<>68 or substr(new.session_key,1,4)<>'sk1_' or substr(new.session_key,5) glob '*[^0-9a-f]*' begin select raise(abort,'session key must be sk1'); end`,
		`create index if not exists idx_entries_session_line on entries(session_key,line_no)`,
		`create index if not exists idx_entries_time_role on entries(timestamp,role)`,
		`create trigger if not exists entries_require_session before insert on entries when not exists(select 1 from sessions where session_key=new.session_key) begin select raise(abort,'entry session missing'); end`,
		`create trigger if not exists entries_require_nonempty before insert on entries when new.session_key='' or new.entry_id='' begin select raise(abort,'entry key required'); end`,
		`create trigger if not exists entries_conflict_before_insert before insert on entries when exists(select 1 from entries where session_key=new.session_key and entry_id=new.entry_id) begin insert into conflicts(session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json) select new.session_key,new.entry_id,e.entry_sha256,new.entry_sha256,json_object('kind','same-session-trigger') from entries e where e.session_key=new.session_key and e.entry_id=new.entry_id and e.entry_sha256<>new.entry_sha256; select raise(ignore); end`,
		`create trigger if not exists entries_no_update before update on entries begin select raise(abort,'entries are append-only'); end`,
		`create trigger if not exists entries_no_delete before delete on entries begin select raise(abort,'entries are append-only'); end`,
		`create trigger if not exists messages_require_entry before insert on messages when not exists(select 1 from entries where session_key=new.session_key and entry_id=new.entry_id) begin select raise(abort,'message entry missing'); end`,
		`create trigger if not exists messages_ai after insert on messages when trim(coalesce(new.text,''))<>'' begin insert into fts_messages(rowid,session_key,entry_id,text) values(new.rowid,new.session_key,new.entry_id,new.text); end`,
		`create trigger if not exists messages_no_update before update on messages begin select raise(abort,'messages are append-only'); end`,
		`create trigger if not exists messages_no_delete before delete on messages begin select raise(abort,'messages are append-only'); end`,
		`create trigger if not exists entry_assets_require_entry before insert on entry_assets when not exists(select 1 from entries where session_key=new.session_key and entry_id=new.entry_id) begin select raise(abort,'entry asset entry missing'); end`,
		`create trigger if not exists artifacts_require_bundle before insert on artifacts when not exists(select 1 from bundles where bundle_id=new.bundle_id) begin select raise(abort,'artifact bundle missing'); end`,
		`create trigger if not exists artifacts_ai after insert on artifacts when trim(coalesce(new.text_body,new.text_preview,''))<>'' begin insert into fts_artifacts(rowid,artifact_id,text) values(new.artifact_id,new.artifact_id,coalesce(new.text_body,new.text_preview,'')); end`,
		`create trigger if not exists artifacts_no_update before update on artifacts begin select raise(abort,'artifacts are append-only'); end`,
		`create trigger if not exists artifacts_no_delete before delete on artifacts begin select raise(abort,'artifacts are append-only'); end`,
		`create trigger if not exists conflicts_no_update before update on conflicts begin select raise(abort,'conflicts are append-only'); end`,
		`create trigger if not exists conflicts_no_delete before delete on conflicts begin select raise(abort,'conflicts are append-only'); end`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return err
		}
	}
	return migrate(db)
}

type migration struct {
	version int
	apply   func(*sql.DB) error
}

var migrations = []migration{
	{version: 2, apply: func(db *sql.DB) error {
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
	}},
	{version: 3, apply: func(db *sql.DB) error {
		stmts := []string{
			`drop trigger if exists messages_ai`,
			`drop trigger if exists artifacts_ai`,
			`delete from fts_messages`,
			`insert into fts_messages(rowid,session_key,entry_id,text) select rowid,session_key,entry_id,text from messages where trim(coalesce(text,''))<>''`,
			`delete from fts_artifacts`,
			`insert into fts_artifacts(rowid,artifact_id,text) select artifact_id,artifact_id,coalesce(nullif(text_body,''),text_preview,'') from artifacts where trim(coalesce(nullif(text_body,''),text_preview,''))<>''`,
			`create trigger messages_ai after insert on messages when trim(coalesce(new.text,''))<>'' begin insert into fts_messages(rowid,session_key,entry_id,text) values(new.rowid,new.session_key,new.entry_id,new.text); end`,
			`create trigger artifacts_ai after insert on artifacts when trim(coalesce(new.text_body,new.text_preview,''))<>'' begin insert into fts_artifacts(rowid,artifact_id,text) values(new.artifact_id,new.artifact_id,coalesce(new.text_body,new.text_preview,'')); end`,
		}
		for _, st := range stmts {
			if _, err := db.Exec(st); err != nil {
				return err
			}
		}
		return nil
	}},
}

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		applied, err := migrationApplied(db, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := m.apply(db); err != nil {
			return err
		}
		if _, err := db.Exec(`insert or ignore into schema_migrations(version) values(?)`, m.version); err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(db *sql.DB, version int) (bool, error) {
	var n int
	if err := db.QueryRow(`select count(*) from schema_migrations where version=?`, version).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
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
