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
	BlobsExisting int  `json:"blobs_existing"`
	BlobsCarried  int  `json:"blobs_carried"`
}

// ManifestSHA256 is the identity of the pushed (or reused) snapshot.
func (r PushResult) ManifestSHA256() model.ManifestSHA256 { return r.manifestSHA }

type PushOptions struct {
	Progress *ahaprogress.Tracker
}

// UploadPlan freezes validated publication inputs behind an opaque type.
type UploadPlan struct {
	writer   PreparedUpload
	manifest model.SnapshotManifest
	source   BlobSource
	summary  UploadPlanSummary
}

type UploadPlanSummary struct {
	Files         int `json:"files"`
	BlobsUpload   int `json:"blobs_upload"`
	BlobsExisting int `json:"blobs_existing"`
	BlobsCarried  int `json:"blobs_carried"`
}

func (p PreparedUpload) PlanUpload(ctx context.Context, manifest model.SnapshotManifest, source BlobSource) (UploadPlan, error) {
	if _, err := p.ArchiveState(); err != nil {
		return UploadPlan{}, err
	}
	if source == nil {
		return UploadPlan{}, fmt.Errorf("Archive upload source is required")
	}
	if _, _, err := model.EncodeSnapshotManifest(manifest); err != nil {
		return UploadPlan{}, err
	}
	machine, err := p.depot.forMachine(manifest.MachineID)
	if err != nil {
		return UploadPlan{}, err
	}
	parent, hasParent, err := machine.parent(ctx)
	if err != nil {
		return UploadPlan{}, err
	}
	if p.machines[manifest.MachineID] && !hasParent {
		return UploadPlan{}, &DamagedArchiveError{Problems: []string{"publishing machine is indexed without a readable latest snapshot"}}
	}
	summary := UploadPlanSummary{Files: len(manifest.Files)}
	seen := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		if seen[file.SHA256] {
			continue
		}
		seen[file.SHA256] = true
		key, err := model.NewBlobKey(file.SHA256)
		if err != nil {
			return UploadPlan{}, err
		}
		if hasParent {
			if _, ok := machine.carriedBlob(parent, key); ok {
				summary.BlobsCarried++
				continue
			}
		}
		exists, err := p.depot.HasBlob(ctx, key)
		if err != nil {
			return UploadPlan{}, err
		}
		if exists {
			summary.BlobsExisting++
		} else {
			summary.BlobsUpload++
		}
	}
	return UploadPlan{writer: p, manifest: manifest, source: source, summary: summary}, nil
}

func (p UploadPlan) Summary() (UploadPlanSummary, error) {
	if _, err := p.writer.ArchiveState(); err != nil || p.source == nil {
		return UploadPlanSummary{}, fmt.Errorf("invalid Archive upload plan")
	}
	return p.summary, nil
}

func (p UploadPlan) Execute(ctx context.Context, opts PushOptions) (PushResult, error) {
	if _, err := p.writer.ArchiveState(); err != nil || p.source == nil {
		return PushResult{}, fmt.Errorf("invalid Archive upload plan")
	}
	return pushV2WithOptions(ctx, p.writer.depot, p.manifest, p.source, p.writer.machines[p.manifest.MachineID], opts)
}

// Push publishes through a capability constructed only after Archive marker
// validation. Upload commands use this method rather than accepting a raw V2.
func (p PreparedUpload) Push(ctx context.Context, manifest model.SnapshotManifest, src BlobSource, opts PushOptions) (PushResult, error) {
	if _, err := p.ArchiveState(); err != nil {
		return PushResult{}, err
	}
	if src == nil {
		return PushResult{}, fmt.Errorf("Archive upload source is required")
	}
	return pushV2WithOptions(ctx, p.depot, manifest, src, p.machines[manifest.MachineID], opts)
}

func pushV2(ctx context.Context, v2 *V2, manifest model.SnapshotManifest, src BlobSource) (PushResult, error) {
	return pushV2WithOptions(ctx, v2, manifest, src, false, PushOptions{})
}

// pushV2WithOptions is deliberately package-private. The only public entry
// points require PreparedUpload or UploadPlan, so an uninitialised or damaged
// Archive cannot be passed to publication code.
func pushV2WithOptions(ctx context.Context, v2 *V2, manifest model.SnapshotManifest, src BlobSource, requireParent bool, opts PushOptions) (PushResult, error) {
	_, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		return PushResult{}, err
	}
	md, err := v2.forMachine(manifest.MachineID)
	if err != nil {
		return PushResult{}, err
	}
	res := PushResult{manifestSHA: sha, Files: len(manifest.Files)}
	parent, hasParent, err := md.parent(ctx)
	if err != nil {
		return res, err
	}
	if requireParent && !hasParent {
		return res, &DamagedArchiveError{Problems: []string{"publishing machine is indexed without a readable latest snapshot"}}
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
			// A verified parent is an immutable receipt: the Archive API has no
			// delete operation. Deep verify owns out-of-band tamper detection.
			if err := md.recommitParent(ctx, parent); err != nil {
				return res, err
			}
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
	receipts := make([]blobReceipt, 0, len(manifest.Files))
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
			if r, ok := md.carriedBlob(parent, key); ok {
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
		r, err := md.ensureBlob(ctx, key, srcPath)
		if err != nil {
			return res, err
		}
		receipts = append(receipts, r)
		switch r.kind {
		case blobReceiptCreated:
			res.BlobsUploaded++
		case blobReceiptExisting:
			res.BlobsExisting++
		}
		processedBlobs++
		opts.Progress.Advance(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
	}
	opts.Progress.Complete(ahaprogress.PhaseUpload, processedBlobs, totalBlobs, ahaprogress.UnitBlobs)
	uploadComplete = true
	opts.Progress.Start(ahaprogress.PhasePublish, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	publishStarted = true
	pub, err := md.publishSnapshot(ctx, manifest, receipts, parent)
	if err != nil {
		return res, err
	}
	if err := md.setLatest(ctx, pub); err != nil {
		return res, err
	}
	opts.Progress.Complete(ahaprogress.PhasePublish, 1, ahaprogress.KnownTotal(1), ahaprogress.UnitSteps)
	publishComplete = true
	return res, nil
}
