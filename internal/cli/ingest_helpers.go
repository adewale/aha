package cli

import (
	"fmt"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/redact"
)

// ingestorForConfig builds an Ingestor wired with the redactor
// implied by cfg.Redaction and cfg.RedactionExtraPatterns. The
// resulting Ingestor's RedactionLevel is stamped on every session
// it ingests.
//
// Levels supported here:
//   - "none-v1" or empty: no redactor (no-op behaviour).
//   - "v1": default 14-pattern redactor plus any extras from config.
//
// Future levels (v1.2 env-file pre-pass, v1.5 audit trail) layer on
// top by composing additional pipeline stages.
func ingestorForConfig(store *corpus.Store, cfg model.Config) (corpus.Ingestor, error) {
	ing := corpus.NewIngestor(store, adapters.Builtins())
	level := cfg.Redaction
	if level == "" {
		level = "none-v1"
	}
	ing.RedactionLevel = level
	switch level {
	case "none-v1":
		// No-op redactor.
	case "v1":
		extras := make([]redact.ExtraPattern, len(cfg.RedactionExtraPatterns))
		for i, p := range cfg.RedactionExtraPatterns {
			extras[i] = redact.ExtraPattern{Name: p.Name, Regex: p.Regex}
		}
		r, err := redact.NewWithExtras(extras)
		if err != nil {
			return corpus.Ingestor{}, fmt.Errorf("redaction config: %w", err)
		}
		ing.Redactor = r
	default:
		return corpus.Ingestor{}, fmt.Errorf("unknown redaction level %q (supported: none-v1, v1)", level)
	}
	return ing, nil
}
