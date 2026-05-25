package corpus

type FTSRepairReport struct {
	DeletedMessageRows   int `json:"deleted_message_rows"`
	InsertedMessageRows  int `json:"inserted_message_rows"`
	DeletedArtifactRows  int `json:"deleted_artifact_rows"`
	InsertedArtifactRows int `json:"inserted_artifact_rows"`
}

func ReconcileFTS(store *Store) error {
	_, err := ReconcileFTSWithReport(store)
	return err
}

func ReconcileFTSWithReport(store *Store) (FTSRepairReport, error) {
	report := FTSRepairReport{}
	var err error
	if report.DeletedMessageRows, err = verifyCount(store.DB, `select count(*) from fts_messages`); err != nil {
		return report, err
	}
	if report.DeletedArtifactRows, err = verifyCount(store.DB, `select count(*) from fts_artifacts`); err != nil {
		return report, err
	}
	if report.InsertedMessageRows, err = verifyCount(store.DB, `select count(*) from messages where trim(coalesce(text,''))<>''`); err != nil {
		return report, err
	}
	if report.InsertedArtifactRows, err = verifyCount(store.DB, `select count(*) from artifacts where trim(coalesce(nullif(text_body,''),text_preview,''))<>''`); err != nil {
		return report, err
	}
	if _, err := store.DB.Exec(`delete from fts_messages`); err != nil {
		return report, err
	}
	if _, err := store.DB.Exec(`insert into fts_messages(rowid,session_key,entry_id,text) select rowid,session_key,entry_id,text from messages where trim(coalesce(text,''))<>''`); err != nil {
		return report, err
	}
	if _, err := store.DB.Exec(`delete from fts_artifacts`); err != nil {
		return report, err
	}
	if _, err := store.DB.Exec(`insert into fts_artifacts(rowid,artifact_id,text) select artifact_id,artifact_id,coalesce(nullif(text_body,''),text_preview,'') from artifacts where trim(coalesce(nullif(text_body,''),text_preview,''))<>''`); err != nil {
		return report, err
	}
	return report, nil
}
