//go:build integration

package depot_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestR2IntegrationV2PushPullVerify is the live-bucket smoke test: the
// full depot v2 contract against a real R2 (or S3-compatible) bucket.
//
//	AHA_R2_TEST_BUCKET=aha-depot-test \
//	AHA_R2_ACCOUNT_ID=... AHA_R2_ACCESS_KEY_ID=... AHA_R2_SECRET_ACCESS_KEY=... \
//	go test -tags integration ./internal/depot/ -run TestR2IntegrationV2 -v
//
// It exercises what the fake cannot vouch for: real conditional writes
// (If-Match/If-None-Match on pointer and index), real strong-consistency
// read-after-write, and content round-trips through the actual service.
// Like every v2 test it asserts the acceptance properties, not just
// liveness: a delta push uploads exactly the delta, and an unchanged
// push with a new timestamp reuses the parent snapshot.
//
// The production v2 surface cannot delete (invariant I5), so the test
// namespaces itself under a unique machine ID and cleans up after itself
// with raw SDK calls — test code, not depot code.
func TestR2IntegrationV2PushPullVerify(t *testing.T) {
	bucket := osGetenvNonEmpty(t, "AHA_R2_TEST_BUCKET")
	cfg, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err != nil {
		t.Skipf("R2 credentials not configured: %v", err)
	}
	r2 := depot.NewR2(bucket, cfg)
	v2 := depot.NewV2FromR2(r2)
	ctx := t.Context()

	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := v2.Init(ctx); err != nil {
		t.Fatalf("re-init must be idempotent: %v", err)
	}

	// Unique machine + unique content per run: parallel/old runs cannot
	// interfere, and deleting our blobs cannot orphan anyone else's
	// manifests (content addresses are unique to this run).
	runID := time.Now().UnixNano()
	machine := fmt.Sprintf("smoke-%d", runID)
	contentA := fmt.Sprintf("smoke session a %d\n", runID)
	contentB := fmt.Sprintf("smoke session b %d\n", runID)
	cleanup := &r2TestCleaner{client: r2.Client, bucket: bucket, machine: machine}
	t.Cleanup(func() { cleanup.run() })

	// First push: everything uploads.
	first, err := depot.PushV2(ctx, v2, snapshotManifestFor(machine, sessionFile(contentA, "a.jsonl")), newMapBlobSource(t, map[string]string{"a.jsonl": contentA}))
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackSnapshot(first.ManifestSHA256())
	cleanup.trackBlob(contentA)
	if first.Reused || first.BlobsUploaded != 1 {
		t.Fatalf("first push: %+v", first)
	}

	// Delta push: exactly the new blob uploads, the old one is carried.
	second, err := depot.PushV2(ctx, v2, snapshotManifestFor(machine, sessionFile(contentA, "a.jsonl"), sessionFile(contentB, "b.jsonl")), newMapBlobSource(t, map[string]string{"b.jsonl": contentB}))
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackSnapshot(second.ManifestSHA256())
	cleanup.trackBlob(contentB)
	if second.Reused || second.BlobsUploaded != 1 || second.BlobsCarried != 1 {
		t.Fatalf("delta push: %+v", second)
	}

	// Unchanged state on a new day: recognized from the pointer alone.
	// The empty blob source proves no content is read or uploaded.
	unchanged := snapshotManifestFor(machine, sessionFile(contentA, "a.jsonl"), sessionFile(contentB, "b.jsonl"))
	unchanged.CapturedAt = "2099-01-01T00:00:00Z"
	reused, err := depot.PushV2(ctx, v2, unchanged, newMapBlobSource(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Reused || reused.ManifestSHA256() != second.ManifestSHA256() {
		t.Fatalf("unchanged push: %+v want reuse of %s", reused, second.ManifestSHA256())
	}

	// Pull side: index -> pointer -> identity-verified manifest -> blobs.
	machines, err := v2.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(machines, machine) {
		t.Fatalf("machines index %v missing %s", machines, machine)
	}
	latest, ok, err := v2.Latest(ctx, machine)
	if err != nil || !ok || latest != second.ManifestSHA256() {
		t.Fatalf("Latest=%v ok=%v err=%v want %s", latest, ok, err, second.ManifestSHA256())
	}
	manifest, err := v2.Manifest(ctx, machine, latest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 || manifest.MachineID != machine {
		t.Fatalf("manifest round trip: machine=%s files=%d", manifest.MachineID, len(manifest.Files))
	}
	for content, name := range map[string]string{contentA: "a.jsonl", contentB: "b.jsonl"} {
		key, err := model.NewBlobKey(hash.SHA256Bytes([]byte(content)))
		if err != nil {
			t.Fatal(err)
		}
		rc, err := v2.OpenBlob(ctx, key)
		if err != nil {
			t.Fatalf("OpenBlob %s: %v", name, err)
		}
		got, err := io.ReadAll(rc)
		if cerr := rc.Close(); cerr != nil {
			t.Fatal(cerr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Fatalf("blob %s round trip: %q", name, got)
		}
	}

	// Audit path against the live service. The bucket is shared, so only
	// problems implicating this run's namespace fail the test.
	for _, deep := range []bool{false, true} {
		report, err := v2.Verify(ctx, deep)
		if err != nil {
			t.Fatalf("verify deep=%v: %v", deep, err)
		}
		for _, p := range report.Problems {
			if strings.Contains(p, machine) || strings.Contains(p, first.ManifestSHA256().String()) || strings.Contains(p, second.ManifestSHA256().String()) {
				t.Fatalf("verify deep=%v flagged this run: %s", deep, p)
			}
		}
	}
}

func osGetenvNonEmpty(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("set %s plus R2/AHA_R2 credentials to run the live-bucket smoke test", key)
	}
	return v
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// r2TestCleaner removes this run's objects with raw SDK calls. Production
// depot code has no delete (I5); tests cleaning their own namespace do.
type r2TestCleaner struct {
	client  *s3.Client
	bucket  string
	machine string
	keys    []string
}

func (c *r2TestCleaner) trackSnapshot(sha model.ManifestSHA256) {
	c.keys = append(c.keys, depot.ManifestObjectKey(c.machine, sha))
}

func (c *r2TestCleaner) trackBlob(content string) {
	key, err := model.NewBlobKey(hash.SHA256Bytes([]byte(content)))
	if err != nil {
		return
	}
	c.keys = append(c.keys, depot.BlobObjectKey(key))
}

func (c *r2TestCleaner) run() {
	ctx := context.Background()
	keys := append([]string{depot.LatestPointerKey(c.machine)}, c.keys...)
	for _, key := range keys {
		// Best-effort: a failed delete only leaves uniquely-named smoke
		// objects behind for the next run's namespace to ignore.
		_, _ = c.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)})
	}
	// Remove this run's machine from the shared index with a conditional
	// read-modify-write; best effort under contention from parallel runs.
	for attempt := 0; attempt < 5; attempt++ {
		obj, err := c.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey)})
		if err != nil {
			return
		}
		b, err := io.ReadAll(obj.Body)
		_ = obj.Body.Close()
		if err != nil {
			return
		}
		machines, err := depot.DecodeMachinesIndex(b)
		if err != nil {
			return
		}
		kept := machines[:0]
		for _, m := range machines {
			if m != c.machine {
				kept = append(kept, m)
			}
		}
		if len(kept) == len(machines) {
			return
		}
		updated, err := depot.EncodeMachinesIndex(kept)
		if err != nil {
			return
		}
		_, err = c.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey), Body: strings.NewReader(string(updated)), ContentType: aws.String("application/json"), IfMatch: obj.ETag})
		if err == nil {
			return
		}
	}
	// Best-effort under contention from parallel runs; a stale index entry
	// is harmless (its pointer is gone, so pulls skip it).
}
