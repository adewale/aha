package depot_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// crashingTransport kills the network from the crashAt-th HTTP request
// onward — the R2 analogue of the local fault sweep, injected below the
// AWS SDK so its internal retries cannot quietly absorb the fault (a
// crashed process does not get a second attempt).
type crashingTransport struct {
	inner   http.RoundTripper
	mu      sync.Mutex
	ops     int
	crashAt int // 0 = never crash; counting still happens
}

var errCrashed = errors.New("injected network crash")

func (c *crashingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.ops++
	dead := c.crashAt > 0 && c.ops >= c.crashAt
	c.mu.Unlock()
	if dead {
		return nil, errCrashed
	}
	return c.inner.RoundTrip(req)
}

func (f *fakeS3) depotV2WithTransport(bucket string, ct *crashingTransport) *depot.V2 {
	ct.inner = http.DefaultTransport
	// No SDK retries: the transport models a crashed process, so retrying
	// only adds backoff sleeps — a dead connection stays dead.
	client := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(f.server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""), HTTPClient: &http.Client{Transport: ct}, Retryer: aws.NopRetryer{}})
	return depot.NewV2FromR2(&depot.R2{Bucket: bucket, Client: client})
}

// runCrashScenario pushes snapshot A then grown snapshot A+B for one
// machine — the same scenario as the local fault sweep — and returns the
// final manifest identity.
func runCrashScenario(t *testing.T, ctx context.Context, v2 *depot.V2) (model.ManifestSHA256, error) {
	t.Helper()
	stateA := map[string]string{"a.jsonl": "crash content a\n"}
	stateAB := map[string]string{"a.jsonl": "crash content a\n", "b.jsonl": "crash content b\n"}
	manifest := func(state map[string]string) model.SnapshotManifest {
		var files []model.ManifestFile
		for name, content := range state {
			files = append(files, sessionFile(content, name))
		}
		return snapshotManifestFor("crash-machine", files...)
	}
	if _, err := depot.PushV2(ctx, v2, manifest(stateA), newMapBlobSource(t, stateA)); err != nil {
		return model.ManifestSHA256{}, err
	}
	res, err := depot.PushV2(ctx, v2, manifest(stateAB), newMapBlobSource(t, stateAB))
	if err != nil {
		return model.ManifestSHA256{}, err
	}
	return res.ManifestSHA256(), nil
}

// TestR2PushSurvivesANetworkCrashAtEveryRequest is the fake-R2 twin of
// the local fault-injection sweep, proving crash-consistency EQUIVALENCE
// across the two drivers: for every HTTP request the push scenario makes,
// kill the network from that request onward and assert the same contract
// the local sweep pins —
//
//   - the crashed push reports an error, never silent partial success;
//   - whatever half-state reached the bucket still deep-verifies clean
//     (no dangling pointer, no manifest missing blobs: I2 under partial
//     failure on the conditional-write driver, not just the local one);
//   - a retry on a healthy connection converges to exactly the state a
//     never-crashed run produces — which is also byte-for-byte the same
//     snapshot identity the LOCAL driver converges to.
func TestR2PushSurvivesANetworkCrashAtEveryRequest(t *testing.T) {
	ctx := context.Background()

	// Cross-driver anchor: the clean scenario's identity on the local
	// driver, which every R2 run (crashed or clean) must converge to.
	wantSHA := func() model.ManifestSHA256 {
		local, err := depot.NewLocalV2(filepath.Join(t.TempDir(), "depot"))
		if err != nil {
			t.Fatal(err)
		}
		if err := local.Init(ctx); err != nil {
			t.Fatal(err)
		}
		sha, err := runCrashScenario(t, ctx, local)
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}()

	// Count the clean scenario's HTTP requests against a fresh fake.
	totalOps := func() int {
		f := newFakeS3(t)
		defer f.Close()
		counter := &crashingTransport{}
		v2 := f.depotV2WithTransport("bucket", counter)
		if err := v2.Init(ctx); err != nil {
			t.Fatal(err)
		}
		initOps := counter.ops
		sha, err := runCrashScenario(t, ctx, v2)
		if err != nil {
			t.Fatal(err)
		}
		if sha != wantSHA {
			t.Fatalf("clean R2 run diverged from local: %s want %s", sha, wantSHA)
		}
		return counter.ops - initOps
	}()
	if totalOps < 5 {
		t.Fatalf("scenario too trivial: %d requests", totalOps)
	}

	for crashAt := 1; crashAt <= totalOps; crashAt++ {
		t.Run(fmt.Sprintf("crash-req-%d", crashAt), func(t *testing.T) {
			f := newFakeS3(t)
			defer f.Close()
			healthy := f.Depot("bucket")
			v2Healthy := depot.NewV2FromR2(healthy)
			if err := v2Healthy.Init(ctx); err != nil {
				t.Fatal(err)
			}
			ct := &crashingTransport{crashAt: crashAt}
			v2Crashing := f.depotV2WithTransport("bucket", ct)
			sha, err := runCrashScenario(t, ctx, v2Crashing)
			if err == nil {
				// SDK-level accounting can shift a crash past the end of
				// the scenario; a fully successful run must equal clean.
				if sha != wantSHA {
					t.Fatalf("un-crashed run diverged: %s want %s", sha, wantSHA)
				}
				return
			}
			if !errors.Is(err, errCrashed) {
				t.Fatalf("scenario failed with a non-injected error: %v", err)
			}
			// Crash consistency on the conditional-write driver.
			report, verr := v2Healthy.Verify(ctx, true)
			if verr != nil {
				t.Fatalf("verify after crash: %v", verr)
			}
			if len(report.Problems) != 0 {
				t.Fatalf("crash at request %d left a corrupt depot: %v", crashAt, report.Problems)
			}
			// Retry on a healthy connection converges to the clean state —
			// the same identity the local driver produces.
			got, err := runCrashScenario(t, ctx, v2Healthy)
			if err != nil {
				t.Fatal(err)
			}
			if got != wantSHA {
				t.Fatalf("retry after crash at request %d converged to %s, want %s", crashAt, got, wantSHA)
			}
		})
	}
}
