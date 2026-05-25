package corpus

func qualify(alias, column string) string {
	if alias == "" {
		return column
	}
	return alias + "." + column
}

func ftsMessageTextPredicate(alias string) string {
	return "trim(coalesce(" + qualify(alias, "text") + ",''))<>''"
}

func ftsArtifactTextExpr(alias string) string {
	return "coalesce(nullif(" + qualify(alias, "text_body") + ",'')," + qualify(alias, "text_preview") + ",'')"
}

func ftsArtifactTextPredicate(alias string) string {
	return "trim(" + ftsArtifactTextExpr(alias) + ")<>''"
}

var (
	missingFTSMessagesQuery  = `select count(*) from messages m left join fts_messages f on f.rowid=m.rowid where ` + ftsMessageTextPredicate("m") + ` and f.rowid is null`
	missingFTSArtifactsQuery = `select count(*) from artifacts a left join fts_artifacts f on f.rowid=a.artifact_id where ` + ftsArtifactTextPredicate("a") + ` and f.rowid is null`
	countIndexableMessages   = `select count(*) from messages where ` + ftsMessageTextPredicate("")
	countIndexableArtifacts  = `select count(*) from artifacts where ` + ftsArtifactTextPredicate("")
)
