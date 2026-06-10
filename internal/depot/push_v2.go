package depot

import (
	"context"
	"fmt"

	"github.com/adewale/aha/internal/model"
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

// PushV2 publishes one machine's state to a depot v2: compute the
// snapshot identity, recognize an unchanged state from the parent
// pointer alone (zero writes, zero content reads), upload only blobs the
// parent does not carry, then publish the manifest and move the pointer
// through the typestate flow (invariant I2).
func PushV2(ctx context.Context, v2 *V2, manifest model.SnapshotManifest, src BlobSource) (PushResult, error) {
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
	}
	pub, err := md.PublishSnapshot(ctx, manifest, receipts)
	if err != nil {
		return res, err
	}
	if err := md.SetLatest(ctx, pub); err != nil {
		return res, err
	}
	return res, nil
}
