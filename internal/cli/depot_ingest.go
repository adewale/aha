package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/depot"
)

func ingestFromDepot(stdout io.Writer, store *corpus.Store, drv depot.Driver, jsonOut bool) ([]map[string]any, error) {
	refs, err := drv.List(context.Background())
	if err != nil {
		return nil, err
	}
	ingested, err := corpus.BundleSHAs(store.DB)
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
		if path == "" {
			path = filepath.Join(tmpDir, ref.BundleSHA256+".tar.zst")
			if err := drv.Fetch(context.Background(), ref, path); err != nil {
				return nil, err
			}
		}
		sha, err := archive.FileSHA256(path)
		if err != nil {
			return nil, err
		}
		if sha != ref.BundleSHA256 {
			return nil, fmt.Errorf("depot bundle sha mismatch: catalog=%s actual=%s", ref.BundleSHA256, sha)
		}
		rep, err := corpus.IngestBundle(store, adapters.Builtins(), path)
		if err != nil {
			return nil, err
		}
		item := map[string]any{"bundle": path, "sha256": ref.BundleSHA256, "sessions": rep.Sessions, "entries": rep.Entries, "messages": rep.Messages, "images": rep.Images, "artifacts": rep.Artifacts, "duplicate": rep.Duplicate}
		if jsonOut {
			reports = append(reports, item)
		} else {
			fmt.Fprintf(stdout, "%s: sessions=%d entries=%d messages=%d images=%d artifacts=%d duplicate=%v\n", path, rep.Sessions, rep.Entries, rep.Messages, rep.Images, rep.Artifacts, rep.Duplicate)
		}
	}
	return reports, nil
}

func pendingDepotRefs(refs []depot.BundleRef, ingested map[string]bool) []depot.BundleRef {
	out := refs[:0]
	for _, ref := range refs {
		if ref.BundleSHA256 == "" || ingested[ref.BundleSHA256] {
			continue
		}
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
