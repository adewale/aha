package depot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// faultyStore wraps an objectStore and fails exactly one operation (the
// failAt-th), counting every primitive call. White-box on purpose: fault
// injection below the typestate API is the only way to test invariant I2
// under partial failure rather than by construction alone.
type faultyStore struct {
	inner  objectStore
	failAt int
	ops    int
}

var errInjected = errors.New("injected fault")

func (f *faultyStore) step() error {
	f.ops++
	if f.ops == f.failAt {
		return errInjected
	}
	return nil
}

func (f *faultyStore) get(ctx context.Context, key string) ([]byte, string, error) {
	if err := f.step(); err != nil {
		return nil, "", err
	}
	return f.inner.get(ctx, key)
}

func (f *faultyStore) getStream(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := f.step(); err != nil {
		return nil, err
	}
	return f.inner.getStream(ctx, key)
}

func (f *faultyStore) putFileIfAbsent(ctx context.Context, key, contentType, srcPath string) (bool, error) {
	if err := f.step(); err != nil {
		return false, err
	}
	return f.inner.putFileIfAbsent(ctx, key, contentType, srcPath)
}

func (f *faultyStore) putBytesIfAbsent(ctx context.Context, key, contentType string, b []byte) (bool, error) {
	if err := f.step(); err != nil {
		return false, err
	}
	return f.inner.putBytesIfAbsent(ctx, key, contentType, b)
}

func (f *faultyStore) putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.inner.putBytesConditional(ctx, key, contentType, b, etag)
}

func (f *faultyStore) exists(ctx context.Context, key string) (bool, error) {
	if err := f.step(); err != nil {
		return false, err
	}
	return f.inner.exists(ctx, key)
}

type pathSource struct{ byKey map[string]string }

func (s pathSource) BlobPath(key model.BlobKey) (string, error) {
	p, ok := s.byKey[key.String()]
	if !ok {
		return "", fmt.Errorf("no content for %s", key)
	}
	return p, nil
}

// TestPushSurvivesAFaultAtEveryOperation is the fault-injection sweep
// (chaos-engineering-in-miniature from the testing-best-practices
// research): for EVERY primitive depot operation a push performs, fail
// exactly that operation once and assert the crash-consistency contract:
//
//   - the failed push reports the injected error (never silent);
//   - the depot still verifies clean — in particular the pointer never
//     references a missing manifest and manifests never reference missing
//     blobs (invariant I2 under partial failure, not just by typestate);
//   - a retried push on the healthy store succeeds and converges to
//     exactly the state a never-failed push would have produced.
func TestPushSurvivesAFaultAtEveryOperation(t *testing.T) {
	ctx := context.Background()
	// First, count the operations of a clean two-snapshot scenario.
	cleanOps := func() int {
		root := filepath.Join(t.TempDir(), "depot")
		counter := &faultyStore{inner: &localStoreV2{root: root}, failAt: -1}
		v2 := &V2{addr: Address{Type: "local", Location: root}, store: counter}
		if err := v2.Init(ctx); err != nil {
			t.Fatal(err)
		}
		runScenario(t, ctx, v2)
		return counter.ops
	}()
	if cleanOps < 5 {
		t.Fatalf("scenario too trivial: %d ops", cleanOps)
	}

	// Reference final state from a clean run.
	wantSHA := func() model.ManifestSHA256 {
		root := filepath.Join(t.TempDir(), "depot")
		v2 := &V2{addr: Address{Type: "local", Location: root}, store: &localStoreV2{root: root}}
		if err := v2.Init(ctx); err != nil {
			t.Fatal(err)
		}
		return runScenario(t, ctx, v2)
	}()

	for failAt := 1; failAt <= cleanOps; failAt++ {
		t.Run(fmt.Sprintf("fail-op-%d", failAt), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "depot")
			healthy := &localStoreV2{root: root}
			faulty := &faultyStore{inner: healthy, failAt: failAt}
			v2Faulty := &V2{addr: Address{Type: "local", Location: root}, store: faulty}
			v2Healthy := &V2{addr: Address{Type: "local", Location: root}, store: healthy}
			if err := v2Healthy.Init(ctx); err != nil {
				t.Fatal(err)
			}
			sha, err := runScenarioErr(ctx, v2Faulty)
			if err == nil {
				// The fault landed after the scenario's last op (Init ops
				// shift counts); a fully successful run must equal clean.
				if sha != wantSHA {
					t.Fatalf("un-faulted run diverged: %s want %s", sha, wantSHA)
				}
				return
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("scenario failed with a non-injected error: %v", err)
			}
			// Crash consistency: whatever half-state exists must verify
			// clean — no dangling pointer, no manifest missing blobs.
			report, verr := v2Healthy.Verify(ctx, true)
			if verr != nil {
				t.Fatalf("verify after fault: %v", verr)
			}
			if len(report.Problems) != 0 {
				t.Fatalf("fault at op %d left a corrupt depot: %v", failAt, report.Problems)
			}
			// Retry on the healthy store converges to the clean state.
			got := runScenario(t, ctx, v2Healthy)
			if got != wantSHA {
				t.Fatalf("retry after fault at op %d converged to %s, want %s", failAt, got, wantSHA)
			}
		})
	}
}

// runScenario pushes snapshot A then grown snapshot A+B for one machine
// and returns the final manifest identity.
func runScenario(t *testing.T, ctx context.Context, v2 *V2) model.ManifestSHA256 {
	t.Helper()
	sha, err := runScenarioErr(ctx, v2)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func runScenarioErr(ctx context.Context, v2 *V2) (model.ManifestSHA256, error) {
	dir, err := os.MkdirTemp("", "fault-src-*")
	if err != nil {
		return model.ManifestSHA256{}, err
	}
	defer os.RemoveAll(dir)
	contents := map[string]string{"a.jsonl": "fault content a\n", "b.jsonl": "fault content b\n"}
	src := pathSource{byKey: map[string]string{}}
	mfs := map[string]model.ManifestFile{}
	for name, content := range contents {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return model.ManifestSHA256{}, err
		}
		sha := hash.SHA256Bytes([]byte(content))
		src.byKey[sha] = p
		mfs[name] = model.ManifestFile{Source: "pi", Kind: "session", RelativePath: "sources/pi/sessions/" + name, RawPath: "/r/" + name, SHA256: sha, Bytes: int64(len(content)), SessionID: strings.TrimSuffix(name, ".jsonl"), CopyState: "stable"}
	}
	manifest := func(files ...string) model.SnapshotManifest {
		m := model.SnapshotManifest{Schema: model.SnapshotManifestSchema, MachineID: "fault-machine", CapturedAt: "2026-06-10T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}}
		for _, name := range files {
			m.Files = append(m.Files, mfs[name])
		}
		return m
	}
	if _, err := PushV2(ctx, v2, manifest("a.jsonl"), src); err != nil {
		return model.ManifestSHA256{}, err
	}
	res, err := PushV2(ctx, v2, manifest("a.jsonl", "b.jsonl"), src)
	if err != nil {
		return model.ManifestSHA256{}, err
	}
	return res.ManifestSHA256(), nil
}
