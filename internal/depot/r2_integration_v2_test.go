//go:build integration

package depot_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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
	cfg := resolveLiveR2Config(t)
	validatedBucket, err := depot.ParseR2Bucket(bucket)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := depot.NewR2(validatedBucket, cfg)
	if err != nil {
		t.Fatal(err)
	}
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
	runID := randomRunID(t)
	machine := "smoke-" + runID
	contentA := fmt.Sprintf("smoke session a %s\n", runID)
	contentB := fmt.Sprintf("smoke session b %s\n", runID)
	cleanup := &r2TestCleaner{t: t, client: r2.S3Client(), bucket: bucket, machine: machine}
	t.Cleanup(func() { cleanup.run() })

	// Pre-register exact cleanup keys so a partial failed push is still cleaned.
	firstManifest := snapshotManifestFor(machine, sessionFile(contentA, "a.jsonl"))
	_, firstSHA, err := model.EncodeSnapshotManifest(firstManifest)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackSnapshot(firstSHA)
	cleanup.trackBlob(contentA)

	// First push: everything uploads.
	first, err := depot.PushV2(ctx, v2, firstManifest, newMapBlobSource(t, map[string]string{"a.jsonl": contentA}))
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.BlobsUploaded != 1 {
		t.Fatalf("first push: %+v", first)
	}

	// Delta push: exactly the new blob uploads, the old one is carried.
	secondManifest := snapshotManifestFor(machine, sessionFile(contentA, "a.jsonl"), sessionFile(contentB, "b.jsonl"))
	_, secondSHA, err := model.EncodeSnapshotManifest(secondManifest)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackSnapshot(secondSHA)
	cleanup.trackBlob(contentB)
	second, err := depot.PushV2(ctx, v2, secondManifest, newMapBlobSource(t, map[string]string{"b.jsonl": contentB}))
	if err != nil {
		t.Fatal(err)
	}
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

// TestR2IntegrationV2ConcurrentFirstPushes proves the shared machine index
// converges under real R2 conditional-write contention, not only under the
// in-repo fake. Every writer has a unique namespace and a deadline.
func TestR2IntegrationV2ConcurrentFirstPushes(t *testing.T) {
	bucketName := osGetenvNonEmpty(t, "AHA_R2_TEST_BUCKET")
	cfg := resolveLiveR2Config(t)
	bucket, err := depot.ParseR2Bucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := depot.NewR2(bucket, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v2 := depot.NewV2FromR2(r2)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := v2.Init(ctx); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	runID := randomRunID(t)
	start := make(chan struct{})
	errs := make([]error, writers)
	machines := make([]string, writers)
	cleaners := make([]*r2TestCleaner, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		machines[i] = fmt.Sprintf("smoke-%s-%d", runID, i)
		content := fmt.Sprintf("concurrent smoke %s %d\n", runID, i)
		manifest := snapshotManifestFor(machines[i], sessionFile(content, "session.jsonl"))
		_, sha, err := model.EncodeSnapshotManifest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		cleaner := &r2TestCleaner{t: t, client: r2.S3Client(), bucket: bucketName, machine: machines[i]}
		cleaner.trackSnapshot(sha)
		cleaner.trackBlob(content)
		cleaners[i] = cleaner
		source := newMapBlobSource(t, map[string]string{"session.jsonl": content})
		wg.Add(1)
		go func(i int, manifest model.SnapshotManifest, source *mapBlobSource) {
			defer wg.Done()
			<-start
			_, errs[i] = depot.PushV2(ctx, v2, manifest, source)
		}(i, manifest, source)
	}
	t.Cleanup(func() {
		for _, cleaner := range cleaners {
			cleaner.run()
		}
	})
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent push %d (%s): %v", i, machines[i], err)
		}
	}
	indexed, err := v2.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, machine := range machines {
		if !containsString(indexed, machine) {
			t.Fatalf("machines index missing concurrent writer %s: %v", machine, indexed)
		}
		if _, ok, err := v2.Latest(ctx, machine); err != nil || !ok {
			t.Fatalf("Latest(%s) ok=%v err=%v", machine, ok, err)
		}
	}
	report, err := v2.Verify(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range report.Problems {
		if strings.Contains(problem, runID) {
			t.Fatalf("deep verify flagged concurrent run: %s", problem)
		}
	}
}

func randomRunID(t *testing.T) string {
	t.Helper()
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(bytes[:])
}

func resolveLiveR2Config(t *testing.T) depot.R2Config {
	t.Helper()
	accessKey := strings.TrimSpace(firstTestEnv("AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(firstTestEnv("AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY"))
	if accessKey == "" || secretKey == "" {
		t.Skip("set AHA_R2_ACCESS_KEY_ID and AHA_R2_SECRET_ACCESS_KEY to run the live R2 test")
	}
	cfg, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err != nil {
		t.Fatalf("invalid supplied R2 configuration: %v", err)
	}
	return cfg
}

func firstTestEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
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
	t       *testing.T
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
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys := append([]string{depot.LatestPointerKey(c.machine)}, c.keys...)
	for _, key := range keys {
		if _, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}); err != nil {
			c.t.Errorf("cleanup delete %s: %v", key, err)
		}
	}
	removed := false
	for attempt := 0; attempt < 12; attempt++ {
		obj, err := c.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey)})
		if err != nil {
			c.t.Errorf("cleanup read machines index: %v", err)
			break
		}
		b, readErr := io.ReadAll(obj.Body)
		_ = obj.Body.Close()
		if readErr != nil {
			c.t.Errorf("cleanup read machines index body: %v", readErr)
			break
		}
		machines, decodeErr := depot.DecodeMachinesIndex(b)
		if decodeErr != nil {
			c.t.Errorf("cleanup decode machines index: %v", decodeErr)
			break
		}
		kept := make([]string, 0, len(machines))
		for _, machine := range machines {
			if machine != c.machine {
				kept = append(kept, machine)
			}
		}
		if len(kept) == len(machines) {
			removed = true
			break
		}
		updated, encodeErr := depot.EncodeMachinesIndex(kept)
		if encodeErr != nil {
			c.t.Errorf("cleanup encode machines index: %v", encodeErr)
			break
		}
		_, putErr := c.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey), Body: strings.NewReader(string(updated)), ContentType: aws.String("application/json"), IfMatch: obj.ETag})
		if putErr == nil {
			removed = true
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.t.Errorf("cleanup machines index: %v", ctx.Err())
			return
		case <-timer.C:
		}
	}
	if !removed {
		c.t.Errorf("cleanup left machine %s in index", c.machine)
	}
	for _, key := range keys {
		if _, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}); err == nil {
			c.t.Errorf("cleanup left object %s", key)
		}
	}
}
