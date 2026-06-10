package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
)

func ingestFromDepotWith(stdout io.Writer, ing corpus.Ingestor, drv depot.Driver, jsonOut bool) ([]map[string]any, error) {
	store := ing.Store
	refs, err := drv.List(context.Background())
	if err != nil {
		return nil, err
	}
	ingested, err := corpus.SnapshotManifestSHAs(store.DB)
	if err != nil {
		return nil, err
	}
	refs = pendingDepotRefs(refs, ingested)
	var reports []map[string]any
	tmpDir, err := os.MkdirTemp("", "aha-depot-ingest-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	for _, ref := range refs {
		ref, err := normalizeDepotRef(ref)
		if err != nil {
			return nil, err
		}
		path := localDepotBundlePath(drv, ref)
		fetched := false
		if path == "" {
			path = filepath.Join(tmpDir, ref.BundleSHA256+".tar.zst")
			if err := drv.Fetch(context.Background(), ref, path); err != nil {
				return nil, err
			}
			fetched = true
		}
		rep, err := ing.IngestBundleWithExpectedSHA(path, ref.BundleSHA256)
		if err != nil {
			return nil, err
		}
		item := map[string]any{"bundle": path, "sha256": ref.BundleSHA256, "bytes": ref.Bytes, "fetched": fetched, "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
		if jsonOut {
			reports = append(reports, item)
		} else {
			fmt.Fprintf(stdout, "%s: bytes=%d fetched=%v sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, ref.Bytes, fetched, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
	}
	return reports, nil
}

// pendingDepotRefs filters catalog refs to those whose snapshot identity
// (canonical manifest sha) the corpus has not ingested. Refs without
// manifest metadata are fetched; ingest itself dedups by identity.
func pendingDepotRefs(refs []depot.BundleRef, ingested map[string]bool) []depot.BundleRef {
	out := refs[:0]
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.BundleSHA256 == "" || seen[ref.BundleSHA256] {
			continue
		}
		if ref.ManifestSHA256 != "" && ingested[ref.ManifestSHA256] {
			continue
		}
		seen[ref.BundleSHA256] = true
		out = append(out, ref)
	}
	return out
}

func normalizeDepotRef(ref depot.BundleRef) (depot.BundleRef, error) {
	expected := depot.BundleKey(ref.BundleSHA256)
	if err := depot.ValidateBundleKey(expected); err != nil {
		return depot.BundleRef{}, fmt.Errorf("invalid depot catalog sha %q: %w", ref.BundleSHA256, err)
	}
	if ref.Key == "" {
		ref.Key = expected
	}
	if ref.Key != expected {
		return depot.BundleRef{}, fmt.Errorf("depot catalog key %q does not match sha %s", ref.Key, ref.BundleSHA256)
	}
	return ref, nil
}
