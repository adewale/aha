package cli

import (
	"os"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// captureDepotR2Config persists only non-secret R2 settings. The internal
// helper retains its historic name while the depot package is being refactored;
// no public surface exposes that vocabulary.
func captureDepotR2Config(cfg *model.Config) error {
	if cfg.Archive.Type != "r2" {
		return nil
	}
	rc, err := depot.ResolveR2Config(cfg.Archive.R2)
	if err != nil {
		return err
	}
	cfg.Archive.R2.AccountID = rc.AccountID()
	if endpoint := firstNonEmpty(os.Getenv("AHA_R2_ENDPOINT"), os.Getenv("R2_ENDPOINT"), cfg.Archive.R2.Endpoint); endpoint != "" {
		cfg.Archive.R2.Endpoint = endpoint
	}
	if rc.Region() == "" || rc.Region() == "auto" {
		cfg.Archive.R2.Region = ""
	} else {
		cfg.Archive.R2.Region = rc.Region()
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func depotUninitialized(report depot.VerifyReport) bool {
	return report.Manifests == 0 && report.Machines == 0 && len(report.Problems) == 1 && report.Problems[0] == "missing Archive marker"
}
