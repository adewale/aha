package depot

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Local struct {
	Root string
}

func NewLocal(root string) (*Local, error) {
	expanded, err := expandLocalRoot(root)
	if err != nil {
		return nil, err
	}
	return &Local{Root: expanded}, nil
}

func (d *Local) Address() Address { return Address{Type: "local", Location: d.Root} }

func (d *Local) Init(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(d.Root, "bundles", "v1"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(d.Root, "catalog", "v1"), 0o755); err != nil {
		return err
	}
	markerPath := filepath.Join(d.Root, "depot.json")
	if b, err := os.ReadFile(markerPath); err == nil {
		return validateMarkerBytes(b)
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeJSONAtomic(markerPath, newMarker())
}

func (d *Local) PutBundle(ctx context.Context, bundlePath string) (BundleRef, bool, error) {
	if err := d.Init(ctx); err != nil {
		return BundleRef{}, false, err
	}
	ref, err := BundleRefFromPath(bundlePath)
	if err != nil {
		return BundleRef{}, false, err
	}
	finalPath := filepath.Join(d.Root, filepath.FromSlash(ref.Key))
	created := false
	if _, err := os.Stat(finalPath); os.IsNotExist(err) {
		if err := copyFile(finalPath, bundlePath); err != nil {
			return BundleRef{}, false, err
		}
		created = true
	} else if err != nil {
		return BundleRef{}, false, err
	}
	if ref.Filename == "" {
		ref.Filename = filepath.Base(bundlePath)
	}
	if err := d.appendCatalog(ref); err != nil {
		return BundleRef{}, false, err
	}
	return ref, created, nil
}

func (d *Local) appendCatalog(ref BundleRef) error {
	return withLocalLock(d.Root, func() error {
		path := filepath.Join(d.Root, filepath.FromSlash(CatalogKey(ref.MachineID)))
		shard := CatalogShard{Schema: CatalogSchema, MachineID: ref.MachineID}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &shard)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if shard.Schema == "" {
			shard.Schema = CatalogSchema
		}
		if shard.MachineID == "" {
			shard.MachineID = ref.MachineID
		}
		shard.Bundles = mergeBundleRef(shard.Bundles, ref)
		sortRefs(shard.Bundles)
		return writeJSONAtomic(path, shard)
	})
}

func (d *Local) List(ctx context.Context) ([]BundleRef, error) {
	root := filepath.Join(d.Root, "catalog", "v1")
	var out []BundleRef
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var shard CatalogShard
		if err := json.Unmarshal(b, &shard); err != nil {
			return err
		}
		out = append(out, shard.Bundles...)
		return nil
	}); err != nil {
		return nil, err
	}
	sortRefs(out)
	return out, nil
}

func (d *Local) Fetch(ctx context.Context, ref BundleRef, dst string) error {
	key := ref.Key
	if key == "" {
		key = BundleKey(ref.BundleSHA256)
	}
	if err := ValidateBundleKey(key); err != nil {
		return err
	}
	return copyFile(dst, filepath.Join(d.Root, filepath.FromSlash(key)))
}

func (d *Local) Verify(ctx context.Context, repair bool) (VerifyReport, error) {
	report := VerifyReport{}
	markerPath := filepath.Join(d.Root, "depot.json")
	if b, err := os.ReadFile(markerPath); err == nil {
		if err := validateMarkerBytes(b); err != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+err.Error())
		}
	} else if os.IsNotExist(err) {
		report.Problems = append(report.Problems, "missing depot marker")
		if repair {
			if err := writeJSONAtomic(markerPath, newMarker()); err != nil {
				return report, err
			}
		}
	} else {
		return report, err
	}
	refsBySHA := map[string]BundleRef{}
	bundleRoot := filepath.Join(d.Root, "bundles", "v1")
	if err := filepath.WalkDir(bundleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".tar.zst") {
			return nil
		}
		ref, err := BundleRefFromPath(path)
		if err != nil {
			report.Problems = append(report.Problems, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		rel, _ := filepath.Rel(d.Root, path)
		if filepath.ToSlash(rel) != BundleKey(ref.BundleSHA256) {
			report.Problems = append(report.Problems, fmt.Sprintf("bundle key/content sha mismatch %s", path))
		}
		refsBySHA[ref.BundleSHA256] = ref
		report.Bundles++
		return nil
	}); err != nil {
		return report, err
	}
	catalogRefs, err := d.List(ctx)
	if err != nil {
		if !repair {
			return report, err
		}
		report.Problems = append(report.Problems, err.Error())
	}
	report.Catalogs = len(catalogRefs)
	catalogBySHA := map[string]bool{}
	for _, ref := range catalogRefs {
		catalogBySHA[ref.BundleSHA256] = true
		if _, ok := refsBySHA[ref.BundleSHA256]; !ok {
			report.Problems = append(report.Problems, "catalog references missing bundle "+ref.BundleSHA256)
		}
	}
	for sha := range refsBySHA {
		if !catalogBySHA[sha] {
			report.Problems = append(report.Problems, "catalog missing bundle "+sha)
		}
	}
	if repair {
		if err := withLocalLock(d.Root, func() error {
			catalogRoot := filepath.Join(d.Root, "catalog", "v1")
			_ = os.RemoveAll(catalogRoot)
			if err := os.MkdirAll(catalogRoot, 0o755); err != nil {
				return err
			}
			byMachine := map[string][]BundleRef{}
			for _, ref := range refsBySHA {
				byMachine[ref.MachineID] = mergeBundleRef(byMachine[ref.MachineID], ref)
			}
			for machine, refs := range byMachine {
				sortRefs(refs)
				if err := writeJSONAtomic(filepath.Join(d.Root, filepath.FromSlash(CatalogKey(machine))), CatalogShard{Schema: CatalogSchema, MachineID: machine, Bundles: refs}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return report, err
		}
		report.Repaired = true
	}
	return report, nil
}
