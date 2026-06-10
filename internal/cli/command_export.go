package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// cmdExport materializes one machine's latest snapshot as a portable v1
// bundle.tar.zst — the single-file hand-off format. Together with
// `aha ingest <bundle.tar.zst>` it is the only place the v1 bundle format
// survives (docs/depot-v2-spec.md).
func cmdExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "JSONC config path")
	machine := fs.String("machine", "", "machine whose latest snapshot to export (default: this machine)")
	depotAddr := fs.String("depot", "", "depot address")
	out := fs.String("out", "", "output bundle path (default: aha-export-<machine>-<sha12>.tar.zst)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	target := *machine
	if target == "" {
		target = cfg.MachineID
	}
	if target == "" {
		return fmt.Errorf("machine required: set config machine_id or pass --machine")
	}
	v2, err := depotV2ForConfig(cfg, *depotAddr)
	if err != nil {
		return err
	}
	ctx := context.Background()
	sha, ok, err := v2.Latest(ctx, target)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("depot has no snapshot for machine %q", target)
	}
	manifest, err := v2.Manifest(ctx, target, sha)
	if err != nil {
		return err
	}
	outPath := *out
	if outPath == "" {
		outPath = fmt.Sprintf("aha-export-%s-%s.tar.zst", safeName(target), sha.String()[:12])
	}
	tmpDir, err := os.MkdirTemp("", "aha-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	bundle, err := bundleFromSnapshot(ctx, v2, manifest, sha, tmpDir)
	if err != nil {
		return err
	}
	info, err := archive.WriteWithInfo(outPath, bundle)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"bundle": outPath, "sha256": info.BundleSHA256, "manifest_sha256": sha.String(), "machine": target, "files": len(manifest.Files), "bytes": info.SizeBytes})
	}
	fmt.Fprintf(stdout, "%s\nsha256:%s\n", outPath, info.BundleSHA256)
	return nil
}

// bundleFromSnapshot fetches every blob the snapshot names and assembles a
// v1 bundle: same files, same policy, bundle_id derived from the snapshot
// identity so the export is deterministic for a given snapshot.
func bundleFromSnapshot(ctx context.Context, v2 *depot.V2, manifest model.SnapshotManifest, sha model.ManifestSHA256, tmpDir string) (archive.Bundle, error) {
	var files []model.CapturedFile
	var total int64
	sessionFiles, artifactFiles, imageFiles := 0, 0, 0
	for i, mf := range manifest.Files {
		key, err := model.NewBlobKey(mf.SHA256)
		if err != nil {
			return archive.Bundle{}, err
		}
		rc, err := v2.OpenBlob(ctx, key)
		if err != nil {
			return archive.Bundle{}, err
		}
		path := filepath.Join(tmpDir, fmt.Sprintf("blob-%d", i))
		f, err := os.Create(path)
		if err != nil {
			_ = rc.Close()
			return archive.Bundle{}, err
		}
		_, copyErr := io.Copy(f, rc)
		closeErr := f.Close()
		if rcErr := rc.Close(); copyErr == nil && rcErr != nil {
			copyErr = rcErr
		}
		if copyErr != nil {
			return archive.Bundle{}, copyErr
		}
		if closeErr != nil {
			return archive.Bundle{}, closeErr
		}
		files = append(files, model.CapturedFile{Manifest: mf, Path: path})
		total += mf.Bytes
		switch mf.Kind {
		case "session":
			sessionFiles++
		case "artifact":
			artifactFiles++
		default:
			imageFiles++
		}
	}
	v1 := model.Manifest{
		Schema:         model.BundleSchema,
		BundleID:       "export-" + sha.String()[:16],
		MachineID:      manifest.MachineID,
		MachineLabel:   manifest.MachineLabel,
		CapturedAt:     manifest.CapturedAt,
		CreatedBy:      "aha " + model.Version,
		Implementation: model.Implementation{Language: "go", Archive: "tar.zst"},
		Source:         model.ManifestSource{HostOS: runtime.GOOS},
		Policy:         manifest.Policy,
		Counts:         model.ManifestCounts{SessionFiles: sessionFiles, ArtifactFiles: artifactFiles, ImageFiles: imageFiles, BytesUncompressed: total},
		Adapters:       manifest.Adapters,
		Files:          manifest.Files,
	}
	return archive.Bundle{Manifest: v1, Files: files}, nil
}
