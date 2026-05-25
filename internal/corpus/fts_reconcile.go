package corpus

import "database/sql"

type FTSRepairReport struct {
	DeletedMessageRows   int `json:"deleted_message_rows"`
	InsertedMessageRows  int `json:"inserted_message_rows"`
	DeletedArtifactRows  int `json:"deleted_artifact_rows"`
	InsertedArtifactRows int `json:"inserted_artifact_rows"`
}

var (
	deleteFTSMessagesSQL    = `delete from fts_messages`
	deleteFTSArtifactsSQL   = `delete from fts_artifacts`
	reinsertFTSMessagesSQL  = `insert into fts_messages(rowid,session_key,entry_id,text) select rowid,session_key,entry_id,text from messages where ` + ftsMessageTextPredicate("")
	reinsertFTSArtifactsSQL = `insert into fts_artifacts(rowid,artifact_id,text) select artifact_id,artifact_id,` + ftsArtifactTextExpr("") + ` from artifacts where ` + ftsArtifactTextPredicate("")
)

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
	if report.InsertedMessageRows, err = verifyCount(store.DB, countIndexableMessages); err != nil {
		return report, err
	}
	if report.InsertedArtifactRows, err = verifyCount(store.DB, countIndexableArtifacts); err != nil {
		return report, err
	}
	if err := rebuildFTSMessages(store.DB); err != nil {
		return report, err
	}
	if err := rebuildFTSArtifacts(store.DB); err != nil {
		return report, err
	}
	return report, nil
}

func rebuildFTSMessages(db execer) error {
	if _, err := db.Exec(deleteFTSMessagesSQL); err != nil {
		return err
	}
	_, err := db.Exec(reinsertFTSMessagesSQL)
	return err
}

func rebuildFTSArtifacts(db execer) error {
	if _, err := db.Exec(deleteFTSArtifactsSQL); err != nil {
		return err
	}
	_, err := db.Exec(reinsertFTSArtifactsSQL)
	return err
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}
