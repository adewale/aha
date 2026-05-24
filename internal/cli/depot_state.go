package cli

import (
	"context"
	"path/filepath"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func findDepotBundleWithSameState(ctx context.Context, drv depot.Driver, manifest model.Manifest, tmpDir string) (depot.BundleRef, bool, error) {
	refs, err := drv.List(ctx)
	if err != nil {
		return depot.BundleRef{}, false, err
	}
	want := archive.ManifestStateSHA256(manifest)
	for i := len(refs) - 1; i >= 0; i-- {
		if refs[i].MachineID != manifest.MachineID {
			continue
		}
		ref, err := normalizeDepotRef(refs[i])
		if err != nil {
			return depot.BundleRef{}, false, err
		}
		path := localDepotBundlePath(drv, ref)
		if path == "" {
			path = filepath.Join(tmpDir, "state-"+ref.BundleSHA256+".tar.zst")
			if err := drv.Fetch(ctx, ref, path); err != nil {
				return depot.BundleRef{}, false, err
			}
		}
		sha, err := archive.FileSHA256(path)
		if err != nil {
			return depot.BundleRef{}, false, err
		}
		if sha != ref.BundleSHA256 {
			return depot.BundleRef{}, false, nil
		}
		got, err := archive.ReadManifest(path)
		if err != nil {
			return depot.BundleRef{}, false, err
		}
		if archive.ManifestStateSHA256(got) == want {
			return ref, true, nil
		}
	}
	return depot.BundleRef{}, false, nil
}
