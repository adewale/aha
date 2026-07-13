package depot_test

import (
	"context"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func pushPrepared(ctx context.Context, archive *depot.V2, manifest model.SnapshotManifest, source depot.BlobSource) (depot.PushResult, error) {
	return pushPreparedOptions(ctx, archive, manifest, source, depot.PushOptions{})
}

func pushPreparedOptions(ctx context.Context, archive *depot.V2, manifest model.SnapshotManifest, source depot.BlobSource, opts depot.PushOptions) (depot.PushResult, error) {
	writer, err := archive.PrepareUpload(ctx)
	if err != nil {
		return depot.PushResult{}, err
	}
	return writer.Push(ctx, manifest, source, opts)
}
