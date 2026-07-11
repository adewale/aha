package corpus

import (
	"context"
	"database/sql"
)

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
	return ReconcileFTSWithReportContext(context.Background(), store)
}

func ReconcileFTSWithReportContext(ctx context.Context, store *Store) (FTSRepairReport, error) {
	report := FTSRepairReport{}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	if report.DeletedMessageRows, err = verifyCountContext(ctx, tx, `select count(*) from fts_messages`); err != nil {
		return report, err
	}
	if report.DeletedArtifactRows, err = verifyCountContext(ctx, tx, `select count(*) from fts_artifacts`); err != nil {
		return report, err
	}
	if report.InsertedMessageRows, err = verifyCountContext(ctx, tx, countIndexableMessages); err != nil {
		return report, err
	}
	if report.InsertedArtifactRows, err = verifyCountContext(ctx, tx, countIndexableArtifacts); err != nil {
		return report, err
	}
	if _, err := tx.ExecContext(ctx, deleteFTSMessagesSQL); err != nil {
		return report, err
	}
	if _, err := tx.ExecContext(ctx, reinsertFTSMessagesSQL); err != nil {
		return report, err
	}
	if _, err := tx.ExecContext(ctx, deleteFTSArtifactsSQL); err != nil {
		return report, err
	}
	if _, err := tx.ExecContext(ctx, reinsertFTSArtifactsSQL); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
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
