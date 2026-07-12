//go:build integration

package depot_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	r2SmoketestAttestationKey  = "aha-r2-smoketest-target-v1.json"
	pinnedR2SmoketestBucket    = "aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd"
	pinnedR2SmoketestAccountID = "8837d43caf5a2ab3df5143eb3e2f1b96"
	pinnedR2SmoketestTargetID  = "f7a6d43e8c1b49b0a2d58e7f31c60492"
)

type r2SmoketestAttestation struct {
	Schema    string `json:"schema"`
	TargetID  string `json:"target_id"`
	Bucket    string `json:"bucket"`
	AccountID string `json:"account_id"`
	Endpoint  string `json:"endpoint"`
}

func decodeR2SmoketestAttestation(body []byte) (r2SmoketestAttestation, error) {
	var attestation r2SmoketestAttestation
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&attestation); err != nil {
		return r2SmoketestAttestation{}, fmt.Errorf("invalid R2 smoketest target attestation: %w", err)
	}
	if dec.More() || attestation.Schema != "aha.r2-smoketest-target.v1" || strings.TrimSpace(attestation.TargetID) == "" || strings.TrimSpace(attestation.Bucket) == "" {
		return r2SmoketestAttestation{}, fmt.Errorf("invalid R2 smoketest target attestation")
	}
	return attestation, nil
}

func (a r2SmoketestAttestation) matches(targetID, bucket, accountID, endpoint string) error {
	if a.TargetID != targetID || a.Bucket != bucket || a.AccountID != accountID || strings.TrimRight(a.Endpoint, "/") != strings.TrimRight(endpoint, "/") {
		return fmt.Errorf("R2 smoketest target attestation does not match the requested bucket/account/endpoint")
	}
	return nil
}

type attestedR2Target struct{ r2 *depot.R2 }

func attestLiveR2Target(ctx context.Context, r2 *depot.R2, cfg depot.R2Config) (attestedR2Target, error) {
	targetID := pinnedR2SmoketestTargetID
	object, err := r2.S3Client().GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r2.Bucket().String()), Key: aws.String(r2SmoketestAttestationKey)})
	if err != nil {
		return attestedR2Target{}, fmt.Errorf("read R2 smoketest target attestation before mutation: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(object.Body, 8193))
	closeErr := object.Body.Close()
	if readErr != nil {
		return attestedR2Target{}, readErr
	}
	if closeErr != nil {
		return attestedR2Target{}, closeErr
	}
	if len(body) > 8192 {
		return attestedR2Target{}, fmt.Errorf("R2 smoketest target attestation exceeds 8192 bytes")
	}
	attestation, err := decodeR2SmoketestAttestation(body)
	if err != nil {
		return attestedR2Target{}, err
	}
	if err := attestation.matches(targetID, r2.Bucket().String(), cfg.AccountID(), cfg.Endpoint()); err != nil {
		return attestedR2Target{}, err
	}
	return attestedR2Target{r2: r2}, nil
}

// TestR2IntegrationV2PushPullVerify is the live-bucket smoke test: the
// full depot v2 contract against a real R2 (or S3-compatible) bucket.
//
//	AHA_R2_SMOKETEST_ACCESS_KEY_ID=... \
//	AHA_R2_SMOKETEST_SECRET_ACCESS_KEY=... \
//	go test -tags integration ./internal/depot/ -run TestR2IntegrationV2 -v
//
// The bucket, account, endpoint derivation, and target attestation ID are
// compile-time pinned above; direct test invocation cannot select another
// remote target.
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
func TestDecodeR2SmoketestAttestationBindsTargetIdentity(t *testing.T) {
	const body = `{"schema":"aha.r2-smoketest-target.v1","target_id":"target-1","bucket":"test-bucket","account_id":"0123456789abcdef0123456789abcdef","endpoint":""}`
	attestation, err := decodeR2SmoketestAttestation([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := attestation.matches("target-1", "test-bucket", "0123456789abcdef0123456789abcdef", ""); err != nil {
		t.Fatal(err)
	}
	for name, targetID := range map[string]string{"wrong-target": "target-2", "wrong-bucket": "target-1"} {
		t.Run(name, func(t *testing.T) {
			bucket := "test-bucket"
			if name == "wrong-bucket" {
				bucket = "production-bucket"
			}
			if err := attestation.matches(targetID, bucket, "0123456789abcdef0123456789abcdef", ""); err == nil {
				t.Fatal("mismatched target was accepted")
			}
		})
	}
}

func TestR2CleanupAbsenceRequiresTypedNotFound(t *testing.T) {
	if !isR2TestNotFound(&smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}) {
		t.Fatal("NoSuchKey was not recognised")
	}
	if isR2TestNotFound(&smithy.GenericAPIError{Code: "AccessDenied", Message: "denied"}) {
		t.Fatal("authorisation failure was mistaken for absence")
	}
}

func TestR2IntegrationV2PushPullVerify(t *testing.T) {
	bucket := pinnedR2SmoketestBucket
	cfg := resolveLiveR2Config(t)
	validatedBucket, err := depot.ParseR2Bucket(bucket)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := depot.NewR2(validatedBucket, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	attested, err := attestLiveR2Target(ctx, r2, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v2 := depot.NewV2FromR2(attested.r2)

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

	// Unchanged state on a new day: recognised from the pointer alone.
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
	bucketName := pinnedR2SmoketestBucket
	cfg := resolveLiveR2Config(t)
	bucket, err := depot.ParseR2Bucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := depot.NewR2(bucket, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	attested, err := attestLiveR2Target(ctx, r2, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v2 := depot.NewV2FromR2(attested.r2)
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
	accessKey := osGetenvNonEmpty(t, "AHA_R2_SMOKETEST_ACCESS_KEY_ID")
	secretKey := osGetenvNonEmpty(t, "AHA_R2_SMOKETEST_SECRET_ACCESS_KEY")
	if productionName := matchingAmbientProductionCredential(accessKey, secretKey); productionName != "" {
		t.Fatalf("smoketest credential matches ambient production variable %s; use a distinct bucket-scoped test token", productionName)
	}
	credentials, err := depot.NewR2Credentials(accessKey, secretKey)
	if err != nil {
		t.Fatalf("invalid smoketest R2 credentials: %v", err)
	}
	cfg := model.R2ArchiveConfig{AccountID: pinnedR2SmoketestAccountID, Region: "auto"}
	resolved, err := depot.ResolveR2ConfigExplicit(cfg, credentials)
	if err != nil {
		t.Fatalf("invalid explicit smoketest R2 configuration: %v", err)
	}
	return resolved
}

func matchingAmbientProductionCredential(accessKey, secretKey string) string {
	for _, key := range []string{"AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"} {
		if value := os.Getenv(key); value != "" && value == accessKey {
			return key
		}
	}
	for _, key := range []string{"AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"} {
		if value := os.Getenv(key); value != "" && value == secretKey {
			return key
		}
	}
	return ""
}

func TestR2SmoketestCredentialsRejectAmbientProductionReuse(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "same-access-canary")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "same-secret-canary")
	if got := matchingAmbientProductionCredential("same-access-canary", "different-secret"); got != "AHA_R2_ACCESS_KEY_ID" {
		t.Fatalf("matching access key returned %q", got)
	}
	if got := matchingAmbientProductionCredential("different-access", "same-secret-canary"); got != "AHA_R2_SECRET_ACCESS_KEY" {
		t.Fatalf("matching secret returned %q", got)
	}
}

func osGetenvNonEmpty(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("set %s plus the AHA_R2_SMOKETEST_* target capability to run the live-bucket smoke test", key)
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

// discoveryRemovedCleaner is the only type that exposes namespace deletion.
// A cleanup cannot delete pointers/manifests/blobs until index removal has
// succeeded, so an interrupted cleanup cannot leave an indexed dead namespace.
type discoveryRemovedCleaner struct{ cleaner *r2TestCleaner }

func (c *r2TestCleaner) removeDiscovery(ctx context.Context) (discoveryRemovedCleaner, error) {
	for attempt := 0; attempt < 12; attempt++ {
		obj, err := c.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey)})
		if err != nil {
			return discoveryRemovedCleaner{}, fmt.Errorf("cleanup read machines index: %w", err)
		}
		b, readErr := io.ReadAll(obj.Body)
		closeErr := obj.Body.Close()
		if readErr != nil {
			return discoveryRemovedCleaner{}, fmt.Errorf("cleanup read machines index body: %w", readErr)
		}
		if closeErr != nil {
			return discoveryRemovedCleaner{}, fmt.Errorf("cleanup close machines index body: %w", closeErr)
		}
		machines, err := depot.DecodeMachinesIndex(b)
		if err != nil {
			return discoveryRemovedCleaner{}, fmt.Errorf("cleanup decode machines index: %w", err)
		}
		kept := make([]string, 0, len(machines))
		for _, machine := range machines {
			if machine != c.machine {
				kept = append(kept, machine)
			}
		}
		if len(kept) == len(machines) {
			return discoveryRemovedCleaner{cleaner: c}, nil
		}
		updated, err := depot.EncodeMachinesIndex(kept)
		if err != nil {
			return discoveryRemovedCleaner{}, err
		}
		_, err = c.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(depot.MachinesIndexKey), Body: strings.NewReader(string(updated)), ContentType: aws.String("application/json"), IfMatch: obj.ETag})
		if err == nil {
			return discoveryRemovedCleaner{cleaner: c}, nil
		}
		if attempt+1 == 12 {
			return discoveryRemovedCleaner{}, fmt.Errorf("cleanup machine-index contention: %w", err)
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return discoveryRemovedCleaner{}, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}

func (s discoveryRemovedCleaner) deleteNamespace(ctx context.Context) error {
	c := s.cleaner
	if c == nil {
		return fmt.Errorf("cleanup requires discovery removal proof")
	}
	keys := append([]string{depot.LatestPointerKey(c.machine)}, c.keys...)
	for _, key := range keys {
		if _, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}); err != nil {
			return fmt.Errorf("cleanup delete %s: %w", key, err)
		}
	}
	for _, key := range keys {
		_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)})
		if err == nil {
			return fmt.Errorf("cleanup left object %s", key)
		}
		if !isR2TestNotFound(err) {
			return fmt.Errorf("cleanup confirm absence %s: %w", key, err)
		}
	}
	return nil
}

func isR2TestNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(apiErr.ErrorCode()) {
		case "nosuchkey", "notfound", "404":
			return true
		}
	}
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == 404
}

func (c *r2TestCleaner) run() {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, err := c.removeDiscovery(ctx)
	if err != nil {
		c.t.Errorf("cleanup preserved namespace because discovery removal failed: %v", err)
		return
	}
	if err := state.deleteNamespace(ctx); err != nil {
		c.t.Errorf("cleanup namespace: %v", err)
	}
}
