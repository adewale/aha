package corpus

func ReconcileFTS(store *Store) error {
	if _, err := store.DB.Exec(`delete from fts_messages`); err != nil {
		return err
	}
	if _, err := store.DB.Exec(`insert into fts_messages(rowid,session_key,entry_id,text) select rowid,session_key,entry_id,text from messages where trim(coalesce(text,''))<>''`); err != nil {
		return err
	}
	if _, err := store.DB.Exec(`delete from fts_artifacts`); err != nil {
		return err
	}
	if _, err := store.DB.Exec(`insert into fts_artifacts(rowid,artifact_id,text) select artifact_id,artifact_id,coalesce(nullif(text_body,''),text_preview,'') from artifacts where trim(coalesce(nullif(text_body,''),text_preview,''))<>''`); err != nil {
		return err
	}
	return nil
}
