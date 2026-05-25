package depot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adewale/aha/internal/archive"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

const (
	DefaultR2Bucket = "aha-depot"
	MarkerSchema    = "aha-depot/v1"
	CatalogSchema   = "aha-depot-catalog/v1"
	LayoutVersion   = "v1"
)

type Address struct {
	Type     string
	Location string
}

type BundleRef struct {
	BundleSHA256   string `json:"bundle_sha256"`
	BundleID       string `json:"bundle_id"`
	MachineID      string `json:"machine_id"`
	CapturedAt     string `json:"captured_at"`
	Bytes          int64  `json:"bytes"`
	Sessions       int    `json:"sessions"`
	Filename       string `json:"filename"`
	Key            string `json:"key"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	StateSHA256    string `json:"state_sha256,omitempty"`
}

type CatalogShard struct {
	Schema    string      `json:"schema"`
	MachineID string      `json:"machine_id"`
	Bundles   []BundleRef `json:"bundles"`
}

type VerifyReport struct {
	Bundles  int      `json:"bundles"`
	Catalogs int      `json:"catalogs"`
	Repaired bool     `json:"repaired"`
	Deep     bool     `json:"deep"`
	Problems []string `json:"problems,omitempty"`
}

type Driver interface {
	Address() Address
	Init(ctx context.Context) error
	PutBundle(ctx context.Context, bundlePath string) (BundleRef, bool, error)
	List(ctx context.Context) ([]BundleRef, error)
	Fetch(ctx context.Context, ref BundleRef, dst string) error
	Verify(ctx context.Context, repair bool) (VerifyReport, error)
}

type KnownBundleDriver interface {
	PutBundleKnown(ctx context.Context, bundlePath string, ref BundleRef) (BundleRef, bool, error)
}

type VerifyOptions struct {
	Repair bool
	Deep   bool
}

type OptionsVerifier interface {
	VerifyWithOptions(ctx context.Context, opts VerifyOptions) (VerifyReport, error)
}

func PutBundleKnown(ctx context.Context, d Driver, bundlePath string, ref BundleRef) (BundleRef, bool, error) {
	if kd, ok := d.(KnownBundleDriver); ok {
		return kd.PutBundleKnown(ctx, bundlePath, ref)
	}
	return d.PutBundle(ctx, bundlePath)
}

func VerifyWithOptions(ctx context.Context, d Driver, opts VerifyOptions) (VerifyReport, error) {
	if v, ok := d.(OptionsVerifier); ok {
		return v.VerifyWithOptions(ctx, opts)
	}
	report, err := d.Verify(ctx, opts.Repair)
	report.Deep = true
	return report, err
}

func ParseAddress(s string) (Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Address{Type: "local", Location: "~/.aha/depot"}, nil
	}
	if s == "r2" {
		return Address{Type: "r2", Location: DefaultR2Bucket}, nil
	}
	typ, loc, ok := strings.Cut(s, ":")
	if !ok {
		return Address{Type: "local", Location: s}, nil
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	loc = strings.TrimSpace(loc)
	if typ == "r2" && loc == "" {
		loc = DefaultR2Bucket
	}
	if typ == "local" && loc == "" {
		return Address{}, fmt.Errorf("local depot path required")
	}
	if typ != "local" && typ != "r2" {
		return Address{}, fmt.Errorf("unsupported depot type %q", typ)
	}
	return Address{Type: typ, Location: loc}, nil
}

func AddressFromConfig(cfg model.DepotConfig) Address {
	if cfg.Type == "" {
		return Address{Type: "local", Location: "~/.aha/depot"}
	}
	loc := cfg.Location
	if cfg.Type == "r2" && loc == "" {
		loc = DefaultR2Bucket
	}
	return Address{Type: cfg.Type, Location: loc}
}

func ConfigFromAddress(addr Address) model.DepotConfig {
	return model.DepotConfig{Type: addr.Type, Location: addr.Location}
}

func BundleKey(sha string) string {
	return filepath.ToSlash(filepath.Join("bundles", "v1", sha+".tar.zst"))
}

func ValidateKnownBundleRef(ref BundleRef) error {
	if ref.BundleSHA256 == "" {
		return fmt.Errorf("bundle sha required")
	}
	if ref.Key == "" {
		ref.Key = BundleKey(ref.BundleSHA256)
	}
	if ref.Key != BundleKey(ref.BundleSHA256) {
		return fmt.Errorf("bundle key %q does not match sha %s", ref.Key, ref.BundleSHA256)
	}
	if err := ValidateBundleKey(ref.Key); err != nil {
		return err
	}
	if ref.BundleID == "" {
		return fmt.Errorf("bundle id required")
	}
	if ref.MachineID == "" {
		return fmt.Errorf("machine id required")
	}
	if ref.CapturedAt == "" {
		return fmt.Errorf("captured_at required")
	}
	if ref.Bytes < 0 {
		return fmt.Errorf("bundle bytes must be non-negative")
	}
	return nil
}

func CatalogKey(machine string) string {
	return filepath.ToSlash(filepath.Join("catalog", "v1", safeCatalogComponent(machine)+".json"))
}

func ValidateBundleKey(key string) error {
	if key == "" {
		return fmt.Errorf("empty bundle key")
	}
	if strings.Contains(key, "\\") || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return fmt.Errorf("unsafe bundle key %q", key)
	}
	if !strings.HasPrefix(key, "bundles/v1/") || !strings.HasSuffix(key, ".tar.zst") {
		return fmt.Errorf("unexpected bundle key %q", key)
	}
	sha := strings.TrimSuffix(strings.TrimPrefix(key, "bundles/v1/"), ".tar.zst")
	if len(sha) != 64 {
		return fmt.Errorf("bundle key sha length %d", len(sha))
	}
	for _, r := range sha {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return fmt.Errorf("bundle key sha is not lowercase hex")
		}
	}
	return nil
}

func safeCatalogComponent(machine string) string {
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "machine"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(machine) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" || out == "." || out == ".." {
		sum := sha256.Sum256([]byte(machine))
		return "machine-" + hex.EncodeToString(sum[:8])
	}
	return out
}

func BundleRefFromPath(bundlePath string) (BundleRef, error) {
	manifest, err := archive.ReadManifest(bundlePath)
	if err != nil {
		return BundleRef{}, err
	}
	sha, err := archive.FileSHA256(bundlePath)
	if err != nil {
		return BundleRef{}, err
	}
	st, err := os.Stat(bundlePath)
	if err != nil {
		return BundleRef{}, err
	}
	mb, err := archive.CanonicalManifest(manifest)
	if err != nil {
		return BundleRef{}, err
	}
	return BundleRef{BundleSHA256: sha, BundleID: manifest.BundleID, MachineID: manifest.MachineID, CapturedAt: manifest.CapturedAt, Bytes: st.Size(), Sessions: manifest.Counts.SessionFiles, Filename: filepath.Base(bundlePath), Key: BundleKey(sha), ManifestSHA256: hashBytes(mb), StateSHA256: archive.ManifestStateSHA256(manifest)}, nil
}

func BundleRefFromWriteInfo(manifest model.Manifest, info archive.WriteInfo, filename string) BundleRef {
	return BundleRef{BundleSHA256: info.BundleSHA256, BundleID: manifest.BundleID, MachineID: manifest.MachineID, CapturedAt: manifest.CapturedAt, Bytes: info.SizeBytes, Sessions: manifest.Counts.SessionFiles, Filename: filename, Key: BundleKey(info.BundleSHA256), ManifestSHA256: info.ManifestSHA256, StateSHA256: info.StateSHA256}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mergeBundleRef(list []BundleRef, ref BundleRef) []BundleRef {
	for i := range list {
		if list[i].BundleSHA256 == ref.BundleSHA256 {
			list[i] = mergeRef(list[i], ref)
			return list
		}
	}
	return append(list, ref)
}

func mergeRef(old, new BundleRef) BundleRef {
	if old.BundleID == "" {
		old.BundleID = new.BundleID
	}
	if old.MachineID == "" {
		old.MachineID = new.MachineID
	}
	if old.CapturedAt == "" {
		old.CapturedAt = new.CapturedAt
	}
	if old.Bytes == 0 {
		old.Bytes = new.Bytes
	}
	if old.Sessions == 0 {
		old.Sessions = new.Sessions
	}
	if old.Filename == "" {
		old.Filename = new.Filename
	}
	if old.Key == "" {
		old.Key = new.Key
	}
	if old.ManifestSHA256 == "" {
		old.ManifestSHA256 = new.ManifestSHA256
	}
	if old.StateSHA256 == "" {
		old.StateSHA256 = new.StateSHA256
	}
	return old
}

func sortRefs(refs []BundleRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].CapturedAt != refs[j].CapturedAt {
			return refs[i].CapturedAt < refs[j].CapturedAt
		}
		return refs[i].BundleSHA256 < refs[j].BundleSHA256
	})
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(b)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func copyFile(dst, src string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*.bundle")
	if err != nil {
		return err
	}
	tmp := out.Name()
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func expandLocalRoot(root string) (string, error) {
	if root == "" {
		root = "~/.aha/depot"
	}
	return paths.Expand(root)
}

type marker struct {
	Schema    string `json:"schema"`
	DepotID   string `json:"depot_id"`
	Layout    string `json:"layout"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

func newMarker() marker {
	now := ahaclock.RealClock{}.Now()
	return marker{Schema: MarkerSchema, DepotID: fmt.Sprintf("depot-%d", now.UnixNano()), Layout: LayoutVersion, CreatedAt: now.Format(time.RFC3339), CreatedBy: "aha " + model.Version}
}

func validateMarkerBytes(b []byte) error {
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	return validateMarker(m)
}

func validateMarker(m marker) error {
	if m.Schema != MarkerSchema {
		return fmt.Errorf("depot marker schema %q, want %q", m.Schema, MarkerSchema)
	}
	if m.Layout != LayoutVersion {
		return fmt.Errorf("depot marker layout %q, want %q", m.Layout, LayoutVersion)
	}
	return nil
}
