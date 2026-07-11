package depot

import (
	"context"
	"fmt"

	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

// BlobSource provides the uncompressed bytes of blobs a push needs to
// upload. It is consulted only for content the parent snapshot does not
// already carry, so an unchanged push never reads file content.
type BlobSource interface {
	BlobPath(key model.BlobKey) (string, error)
}

// PushResult reports what one push did.
type PushResult struct {
	manifestSHA   model.ManifestSHA256
	Reused        bool `json:"reused"`
	Files         int  `json:"files"`
	BlobsUploaded int  `json:"blobs_uploaded"`
	BlobsCarried  int  `json:"blobs_carried"`
}

// ManifestSHA256 is the identity of the pushed (or reused) snapshot.
func (r PushResult) ManifestSHA256() model.ManifestSHA256 { return r.manifestSHA }

type PushOptions struct {
	Progress *ahaprogress.Tracker
}

// PushV2 publishes one machine's state to a depot v2. The compatibility
// wrapper keeps progress optional and allocation-free for existing callers.
func PushV2(ctx context.Context, v2 *V2, manifest model.SnapshotManifest, src BlobSource) (PushResult, error) {
	return PushV2WithOptions(ctx, v2, manifest, src, PushOptions{})
}

// PushV2WithOptions computes the snapshot identity, recognizes unchanged
// state from the parent pointer alone, uploads only blobs the parent does not
// carry, then publishes the manifest and pointer through the typestate flow.
func PushV2WithOptions(ctx context.Context, v2 *V2, manifest model.SnapshotManifest, src BlobSource, opts PushOptions) (PushResult, error) {
	_, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		return PushResult{}, err
	}
	md, err := v2.ForMachine(manifest.MachineID)
	if err != nil {
		return PushResult{}, err
	}
	res := PushResult{manifestSHA: sha, Files: len(manifest.Files)}
	parent, hasParent, err := md.Parent(ctx)
	if err != nil {
		return res, err
	}
	if hasParent {
		// Reuse is decided by captured state, not capture time: an
		// unchanged machine refreshed on a new day reuses the parent
		// snapshot and reports the parent's identity.
		parentState, err := model.SnapshotStateSHA256(parent.Manifest())
		if err != nil {
			return res, err
		}
		state, err := model.SnapshotStateSHA256(manifest)
		if err != nil {
			return res, err
		}
		if parentState == state {
			res.manifestSHA = parent.SHA()
			res.Reused = true
			return res, nil
		}
	}
	totalBlobs := ahaprogress.UnknownTotal()
	if opts.Progress != nil {
		unique := make(map[string]bool, len(manifest.Files))
		for _, file := range manifest.Files {
			unique[file.SHA256] = true
		}
		totalBlobs = ahaprogress.KnownTotal(uint64(len(unique)))
	}
	opts.Progress.Start(ahaprogress.PhaseUpload, totalBlobs, ahaprogress.UnitBlobs)
	processedBlobs := uint64(0)
	uploadComplete := false
	publishStarted := false
	publishComplete := false
	defer func() {
		if !uploadComplete {
			if ctx.Err() != nil {
				opts.Progress.Cancel(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
			} else {
				opts.Progress.Fail(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
			}
		} else if publishStarted && !publishComplete {
			if ctx.Err() != nil {
				opts.Progress.Cancel(ahaprogress.PhasePublish, 0, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
			} else {
				opts.Progress.Fail(ahaprogress.PhasePublish, 0, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
			}
		}
	}()
	receipts := make([]BlobReceipt, 0, len(manifest.Files))
	seen := make(map[string]bool, len(manifest.Files))
	for _, f := range manifest.Files {
		if seen[f.SHA256] {
			continue
		}
		seen[f.SHA256] = true
		key, err := model.NewBlobKey(f.SHA256)
		if err != nil {
			return res, fmt.Errorf("manifest file %s: %w", f.RelativePath, err)
		}
		if hasParent {
			if r, ok := md.CarriedBlob(parent, key); ok {
				receipts = append(receipts, r)
				res.BlobsCarried++
				processedBlobs++
				opts.Progress.Advance(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
				continue
			}
		}
		srcPath, err := src.BlobPath(key)
		if err != nil {
			return res, err
		}
		r, err := md.EnsureBlob(ctx, key, srcPath)
		if err != nil {
			return res, err
		}
		receipts = append(receipts, r)
		res.BlobsUploaded++
		processedBlobs++
		opts.Progress.Advance(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
	}
	opts.Progress.Complete(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
	uploadComplete = true
	opts.Progress.Start(ahaprogress.PhasePublish, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	publishStarted = true
	pub, err := md.PublishSnapshot(ctx, manifest, receipts)
	if err != nil {
		return res, err
	}
	if err := md.SetLatest(ctx, pub); err != nil {
		return res, err
	}
	opts.Progress.Complete(ahaprogress.PhasePublish, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	publishComplete = true
	return res, nil
}
