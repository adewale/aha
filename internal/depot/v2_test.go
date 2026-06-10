package depot_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func writeBlobSrc(t *testing.T, dir, name, content string) (string, model.BlobKey) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := model.NewBlobKey(hash.SHA256Bytes([]byte(content)))
	if err != nil {
		t.Fatal(err)
	}
	return p, key
}

func snapshotManifestFor(machine string, files ...model.ManifestFile) model.SnapshotManifest {
	return model.SnapshotManifest{
		Schema:     model.SnapshotManifestSchema,
		MachineID:  machine,
		CapturedAt: "2026-06-09T00:00:00Z",
		CreatedBy:  "aha test",
		Source:     model.ManifestSource{HostOS: "linux"},
		Policy:     model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"},
		Adapters:   []model.ManifestAdapt{{Name: "pi", Version: "test"}},
		Files:      files,
	}
}

func sessionFile(content string, name string) model.ManifestFile {
	return model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/" + name, RawPath: "/raw/" + name, SHA256: hash.SHA256Bytes([]byte(content)), Bytes: int64(len(content)), SessionID: strings.TrimSuffix(name, ".jsonl"), CopyState: "stable"}
}

// pushState publishes one snapshot of the given (name -> content) state and
// returns the manifest sha. It uses carried receipts for files the parent
// already lists, exactly as the real push path does.
func pushState(t *testing.T, ctx context.Context, v2 *depot.V2, machine string, state map[string]string) model.ManifestSHA256 {
	t.Helper()
	md, err := v2.ForMachine(machine)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := md.Parent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var files []model.ManifestFile
	var receipts []depot.BlobReceipt
	names := make([]string, 0, len(state))
	for name := range state {
		names = append(names, name)
	}
	for _, name := range names {
		content := state[name]
		mf := sessionFile(content, name)
		files = append(files, mf)
		key, err := model.NewBlobKey(mf.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if parent != nil {
			if r, ok := md.CarriedBlob(parent, key); ok {
				receipts = append(receipts, r)
				continue
			}
		}
		src, _ := writeBlobSrc(t, dir, name, content)
		r, err := md.EnsureBlob(ctx, key, src)
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, r)
	}
	pub, err := md.PublishSnapshot(ctx, snapshotManifestFor(machine, files...), receipts)
	if err != nil {
		t.Fatal(err)
	}
	if err := md.SetLatest(ctx, pub); err != nil {
		t.Fatal(err)
	}
	return pub.ManifestSHA256()
}

func newLocalV2(t *testing.T) *depot.V2 {
	t.Helper()
	v2, err := depot.NewLocalV2(filepath.Join(t.TempDir(), "depot"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return v2
}

// TestV2LocalPushPullRoundTrip pins the core depot v2 contract: a pushed
// snapshot is discoverable via index -> pointer -> manifest, the manifest's
// identity is verified on read, and blobs come back byte-identical.
func TestV2LocalPushPullRoundTrip(t *testing.T) {
	ctx := context.Background()
	v2 := newLocalV2(t)
	sha := pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "session a bytes"})

	machines, err := v2.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0] != "mach-a" {
		t.Fatalf("Machines=%v want [mach-a]", machines)
	}
	latest, ok, err := v2.Latest(ctx, "mach-a")
	if err != nil || !ok {
		t.Fatalf("Latest ok=%v err=%v", ok, err)
	}
	if latest != sha {
		t.Fatalf("Latest=%s want %s", latest, sha)
	}
	manifest, err := v2.Manifest(ctx, "mach-a", latest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.MachineID != "mach-a" {
		t.Fatalf("manifest round trip: %+v", manifest)
	}
	key, err := model.NewBlobKey(manifest.Files[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := v2.OpenBlob(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if cerr := rc.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "session a bytes" {
		t.Fatalf("blob round trip: %q", got)
	}
}

// TestV2LatestForUnknownMachine pins absent-pointer behavior.
func TestV2LatestForUnknownMachine(t *testing.T) {
	v2 := newLocalV2(t)
	if _, ok, err := v2.Latest(context.Background(), "nobody"); err != nil || ok {
		t.Fatalf("Latest for unknown machine: ok=%v err=%v", ok, err)
	}
	machines, err := v2.Machines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 0 {
		t.Fatalf("Machines=%v want empty", machines)
	}
}

// TestV2PublishRequiresReceiptForEveryFile pins invariant I2: a manifest
// referencing a blob without a receipt cannot be published, so a dangling
// manifest reference is unrepresentable. The failed publish must leave no
// manifest object and no pointer behind.
func TestV2PublishRequiresReceiptForEveryFile(t *testing.T) {
	ctx := context.Background()
	v2 := newLocalV2(t)
	md, err := v2.ForMachine("mach-a")
	if err != nil {
		t.Fatal(err)
	}
	mf := sessionFile("content without a receipt", "a.jsonl")
	if _, err := md.PublishSnapshot(ctx, snapshotManifestFor("mach-a", mf), nil); err == nil {
		t.Fatal("PublishSnapshot accepted a file with no blob receipt")
	}
	if _, ok, err := v2.Latest(ctx, "mach-a"); err != nil || ok {
		t.Fatalf("failed publish moved the pointer: ok=%v err=%v", ok, err)
	}
}

// TestV2PublishRejectsForeignMachine pins invariant I3 at the publish
// boundary: a MachineDepot cannot publish a manifest claiming another
// machine's identity.
func TestV2PublishRejectsForeignMachine(t *testing.T) {
	ctx := context.Background()
	v2 := newLocalV2(t)
	md, err := v2.ForMachine("mach-a")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src, key := writeBlobSrc(t, dir, "a.jsonl", "bytes")
	r, err := md.EnsureBlob(ctx, key, src)
	if err != nil {
		t.Fatal(err)
	}
	mf := sessionFile("bytes", "a.jsonl")
	if _, err := md.PublishSnapshot(ctx, snapshotManifestFor("mach-b", mf), []depot.BlobReceipt{r}); err == nil {
		t.Fatal("mach-a's MachineDepot published a manifest for mach-b")
	}
}

// TestV2SetLatestRejectsZeroValue pins that the pointer can only move to a
// snapshot that actually went through PublishSnapshot.
func TestV2SetLatestRejectsZeroValue(t *testing.T) {
	v2 := newLocalV2(t)
	md, err := v2.ForMachine("mach-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := md.SetLatest(context.Background(), depot.PublishedSnapshot{}); err == nil {
		t.Fatal("SetLatest accepted a zero-value PublishedSnapshot")
	}
}

// TestV2CarriedBlobOnlyForParentListedContent pins that carried receipts
// (skip re-upload) exist only for content the fetched parent manifest
// actually lists.
func TestV2CarriedBlobOnlyForParentListedContent(t *testing.T) {
	ctx := context.Background()
	v2 := newLocalV2(t)
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "first content"})
	md, err := v2.ForMachine("mach-a")
	if err != nil {
		t.Fatal(err)
	}
	parent, ok, err := md.Parent(ctx)
	if err != nil || !ok {
		t.Fatalf("Parent ok=%v err=%v", ok, err)
	}
	listed, err := model.NewBlobKey(hash.SHA256Bytes([]byte("first content")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := md.CarriedBlob(parent, listed); !ok {
		t.Fatal("CarriedBlob refused content the parent lists")
	}
	unlisted, err := model.NewBlobKey(hash.SHA256Bytes([]byte("never pushed")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := md.CarriedBlob(parent, unlisted); ok {
		t.Fatal("CarriedBlob granted a receipt for content the parent does not list")
	}
}

// TestV2BlobAndManifestObjectsAreWriteOnce pins I1/I5 at the driver level:
// re-pushing identical state never rewrites existing objects.
func TestV2BlobAndManifestObjectsAreWriteOnce(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "depot")
	v2, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	state := map[string]string{"a.jsonl": "stable content"}
	sha1 := pushState(t, ctx, v2, "mach-a", state)
	key, _ := model.NewBlobKey(hash.SHA256Bytes([]byte("stable content")))
	blobPath := filepath.Join(root, filepath.FromSlash(depot.BlobObjectKey(key)))
	st1, err := os.Stat(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	sha2 := pushState(t, ctx, v2, "mach-a", state)
	if sha1 != sha2 {
		t.Fatalf("identical state produced different snapshot identities: %s vs %s", sha1, sha2)
	}
	st2, err := os.Stat(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("re-push rewrote an existing blob object")
	}
}

// TestV2InitRefusesV1Depot pins that v2 never silently mixes layouts: a
// directory holding a v1 depot cannot be initialized as v2.
func TestV2InitRefusesV1Depot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "depot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A v1 depot is identified by its depot.json marker.
	if err := os.WriteFile(filepath.Join(root, "depot.json"), []byte(`{"schema":"aha-depot/v1","layout":"v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v2, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(context.Background()); err == nil || !strings.Contains(err.Error(), "v1") {
		t.Fatalf("Init over a v1 depot: err=%v, want v1-layout refusal", err)
	}
}

// TestV2OpenBlobVerifiesContent pins the I1 residual: a corrupted stored
// blob is detected on read.
func TestV2OpenBlobVerifiesContent(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "depot")
	v2, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "honest content"})
	key, _ := model.NewBlobKey(hash.SHA256Bytes([]byte("honest content")))
	blobPath := filepath.Join(root, filepath.FromSlash(depot.BlobObjectKey(key)))
	b, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(blobPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := v2.OpenBlob(ctx, key)
	if err == nil {
		_, err = io.ReadAll(rc)
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		t.Fatal("corrupted blob was read without error")
	}
}

// TestV2ManifestFetchVerifiesIdentity pins that a manifest object whose
// bytes do not hash to its key is rejected on read.
func TestV2ManifestFetchVerifiesIdentity(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "depot")
	v2, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	sha := pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "content"})
	manifestPath := filepath.Join(root, filepath.FromSlash(depot.ManifestObjectKey("mach-a", sha)))
	// Replace with a different but well-formed canonical manifest.
	other, _, err := model.EncodeSnapshotManifest(snapshotManifestFor("mach-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, other, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Manifest(ctx, "mach-a", sha); err == nil {
		t.Fatal("manifest with mismatching identity was accepted")
	}
}

// TestV2R2PushOperationInvariants pins I3 and I6 against the fake R2:
// a push performs no LIST operations and touches no keys outside the
// machine's own namespace, the shared blob/index/marker spaces.
func TestV2R2PushOperationInvariants(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	// Seed a foreign machine so foreign data exists to (not) touch.
	pushState(t, ctx, v2, "mach-other", map[string]string{"o.jsonl": "other machine content"})
	f.resetCounts()
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "mach a content"})
	ops := f.opsSnapshot()
	for op := range ops {
		method, key, _ := strings.Cut(op, " ")
		if method == "LIST" {
			t.Fatalf("push performed a LIST: %q", op)
		}
		allowed := strings.HasPrefix(key, "machines/mach-a/") ||
			key == depot.MachinesIndexKey ||
			strings.HasPrefix(key, "blobs/v2/") ||
			key == depot.MarkerObjectKey
		if !allowed {
			t.Fatalf("push touched a key outside its write set: %q", op)
		}
	}
}

// TestV2R2SecondUnchangedPushWritesNothing pins acceptance property 1
// (steady state): re-pushing identical state performs zero PUTs.
func TestV2R2SecondUnchangedPushWritesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	state := map[string]string{"a.jsonl": "steady content"}
	pushState(t, ctx, v2, "mach-a", state)
	f.resetCounts()
	pushState(t, ctx, v2, "mach-a", state)
	for op := range f.opsSnapshot() {
		if strings.HasPrefix(op, "PUT ") {
			t.Fatalf("unchanged push performed a write: %q", op)
		}
		if strings.HasPrefix(op, "LIST") {
			t.Fatalf("unchanged push performed a LIST: %q", op)
		}
	}
}

// TestV2R2DeltaPushUploadsOnlyTheDelta pins acceptance property 2: a push
// with one new file uploads exactly that blob plus one manifest, and never
// re-uploads carried content.
func TestV2R2DeltaPushUploadsOnlyTheDelta(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "old content"})
	f.resetCounts()
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "old content", "b.jsonl": "new content"})
	var blobPuts, manifestPuts, otherPuts int
	for op, n := range f.opsSnapshot() {
		method, key, _ := strings.Cut(op, " ")
		if method != "PUT" {
			continue
		}
		switch {
		case strings.HasPrefix(key, "blobs/v2/"):
			blobPuts += n
		case strings.HasPrefix(key, "machines/mach-a/manifests/"):
			manifestPuts += n
		case key == depot.LatestPointerKey("mach-a"):
			// pointer move expected
		default:
			otherPuts += n
		}
	}
	if blobPuts != 1 {
		t.Fatalf("delta push uploaded %d blobs, want exactly 1", blobPuts)
	}
	if manifestPuts != 1 {
		t.Fatalf("delta push wrote %d manifests, want exactly 1", manifestPuts)
	}
	if otherPuts != 0 {
		t.Fatalf("delta push performed %d unexpected writes", otherPuts)
	}
}

// TestV2R2RoundTrip exercises the same contract as the local round trip
// against the fake R2, including the machines index across two machines.
func TestV2R2RoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	defer f.Close()
	v2 := depot.NewV2FromR2(f.Depot("bucket"))
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	shaA := pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "content a"})
	pushState(t, ctx, v2, "mach-b", map[string]string{"b.jsonl": "content b"})
	machines, err := v2.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 2 || machines[0] != "mach-a" || machines[1] != "mach-b" {
		t.Fatalf("Machines=%v", machines)
	}
	latest, ok, err := v2.Latest(ctx, "mach-a")
	if err != nil || !ok || latest != shaA {
		t.Fatalf("Latest=%v ok=%v err=%v", latest, ok, err)
	}
	manifest, err := v2.Manifest(ctx, "mach-a", latest)
	if err != nil {
		t.Fatal(err)
	}
	key, err := model.NewBlobKey(manifest.Files[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := v2.OpenBlob(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if cerr := rc.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content a" {
		t.Fatalf("blob round trip over R2: %q", got)
	}
}

// TestV2VerifyReportsMissingBlob pins the audit path: a manifest
// referencing an absent blob is a reported problem.
func TestV2VerifyReportsMissingBlob(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "depot")
	v2, err := depot.NewLocalV2(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	pushState(t, ctx, v2, "mach-a", map[string]string{"a.jsonl": "content"})
	report, err := v2.Verify(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("healthy depot reported problems: %v", report.Problems)
	}
	key, _ := model.NewBlobKey(hash.SHA256Bytes([]byte("content")))
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(depot.BlobObjectKey(key)))); err != nil {
		t.Fatal(err)
	}
	report, err = v2.Verify(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) == 0 {
		t.Fatal("verify missed a missing blob")
	}
}
