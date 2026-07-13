package cli

import (
	"context"
	"errors"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

var errPrivacyAcknowledgement = errors.New("privacy acknowledgement required")

type snapshotRequest struct {
	Context         context.Context
	Config          model.Config
	DepotOverride   string
	CapturedAt      string
	SessionFilters  []string
	MaxSessions     int
	JSON            bool
	Progress        *ahaprogress.Tracker
	ProgressSetting string
	Force           bool
}

func pushResultJSON(res depot.PushResult) map[string]any {
	return map[string]any{
		"manifest_sha256": res.ManifestSHA256().String(),
		"reused":          res.Reused,
		"files":           res.Files,
		"blobs_uploaded":  res.BlobsUploaded,
		"blobs_existing":  res.BlobsExisting,
		"blobs_carried":   res.BlobsCarried,
	}
}
