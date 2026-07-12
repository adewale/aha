package depot_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"pgregory.net/rapid"
)

// TestV2DepotStateMachine drives random op sequences against a local
// depot v2 and checks the model invariants after every step (the v2
// successor of the deleted v1 catalog state-machine test):
//
//   - the pointer, when present, resolves to an identity-verified
//     manifest whose blobs are all present (publish ordering, I2);
//   - every object, once written, never changes bytes again (write-once,
//     I1/I5 — checked against a shadow copy of the object tree);
//   - re-pushing a previously seen state is recognised as reuse with the
//     original identity (state-based reuse);
//   - Verify reports no problems at any point.
func TestV2DepotStateMachine(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		root := filepath.Join(t.TempDir(), "depot")
		v2, err := depot.NewLocalV2(root)
		if err != nil {
			rt.Fatal(err)
		}
		if err := v2.Init(ctx); err != nil {
			rt.Fatal(err)
		}
		machines := []string{"sm-a", "sm-b"}
		// Per machine: history of pushed states and their identities.
		type pushed struct {
			state map[string]string
			sha   model.ManifestSHA256
		}
		history := map[string][]pushed{}
		shadow := map[string]string{} // object path -> content hash at first write
		nextFile := 0

		checkWriteOnce := func() {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || strings.Contains(path, ".tmp") || strings.HasSuffix(path, ".depot.lock") {
					return err
				}
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(b)
				digest := hex.EncodeToString(sum[:])
				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				// Pointer and index are conditional-write objects, not
				// write-once; everything else must never change.
				if strings.HasSuffix(rel, "/latest") || rel == depot.MachinesIndexKey || rel == depot.MarkerObjectKey {
					return nil
				}
				if prev, ok := shadow[rel]; ok && prev != digest {
					rt.Fatalf("write-once violated: %s changed bytes", rel)
				}
				shadow[rel] = digest
				return nil
			})
			if err != nil {
				rt.Fatal(err)
			}
		}

		checkPointer := func(machine string) {
			sha, ok, err := v2.Latest(ctx, machine)
			if err != nil {
				rt.Fatal(err)
			}
			pushes := history[machine]
			if len(pushes) == 0 {
				if ok {
					rt.Fatalf("machine %s has a pointer but never pushed", machine)
				}
				return
			}
			if !ok {
				rt.Fatalf("machine %s pushed but has no pointer", machine)
			}
			want := pushes[len(pushes)-1].sha
			if sha != want {
				rt.Fatalf("machine %s pointer=%s want latest push %s", machine, sha, want)
			}
			manifest, err := v2.Manifest(ctx, machine, sha)
			if err != nil {
				rt.Fatalf("latest manifest unreadable: %v", err)
			}
			for _, f := range manifest.Files {
				key, err := model.NewBlobKey(f.SHA256)
				if err != nil {
					rt.Fatal(err)
				}
				rc, err := v2.OpenBlob(ctx, key)
				if err != nil {
					rt.Fatalf("blob %s of latest manifest missing: %v", key, err)
				}
				if _, err := io.Copy(io.Discard, rc); err != nil {
					rt.Fatalf("blob %s of latest manifest corrupt: %v", key, err)
				}
				_ = rc.Close()
			}
		}

		ops := rapid.SliceOfN(rapid.SampledFrom([]string{"pushNew", "pushGrown", "pushUnchanged", "verify"}), 1, 10).Draw(rt, "ops")
		for _, op := range ops {
			machine := rapid.SampledFrom(machines).Draw(rt, "machine")
			pushes := history[machine]
			var state map[string]string
			switch op {
			case "pushNew":
				nextFile++
				state = map[string]string{fmt.Sprintf("f%d.jsonl", nextFile): fmt.Sprintf("content %s %d", machine, nextFile)}
			case "pushGrown":
				state = map[string]string{}
				if len(pushes) > 0 {
					for k, v := range pushes[len(pushes)-1].state {
						state[k] = v
					}
				}
				nextFile++
				state[fmt.Sprintf("f%d.jsonl", nextFile)] = fmt.Sprintf("content %s %d", machine, nextFile)
			case "pushUnchanged":
				if len(pushes) == 0 {
					continue
				}
				state = pushes[len(pushes)-1].state
			case "verify":
				report, err := v2.Verify(ctx, true)
				if err != nil {
					rt.Fatal(err)
				}
				if len(report.Problems) != 0 {
					rt.Fatalf("verify problems: %v", report.Problems)
				}
				continue
			}
			res, err := pushV2State(ctx, v2, machine, state)
			if err != nil {
				rt.Fatal(err)
			}
			if op == "pushUnchanged" {
				if !res.Reused || res.ManifestSHA256() != pushes[len(pushes)-1].sha {
					rt.Fatalf("unchanged push not reused: %+v", res)
				}
			} else {
				history[machine] = append(pushes, pushed{state: state, sha: res.ManifestSHA256()})
			}
			checkWriteOnce()
			for _, m := range machines {
				checkPointer(m)
			}
		}
	})
}

// pushV2State pushes one (name -> content) state through the real PushV2
// flow with an in-memory blob source.
func pushV2State(ctx context.Context, v2 *depot.V2, machine string, state map[string]string) (depot.PushResult, error) {
	var files []model.ManifestFile
	src := &stateBlobSource{dir: os.TempDir(), byKey: map[string][]byte{}}
	for name, content := range state {
		files = append(files, sessionFile(content, name))
		src.byKey[hash.SHA256Bytes([]byte(content))] = []byte(content)
	}
	return depot.PushV2(ctx, v2, snapshotManifestFor(machine, files...), src)
}

type stateBlobSource struct {
	dir   string
	byKey map[string][]byte
}

func (s *stateBlobSource) BlobPath(key model.BlobKey) (string, error) {
	b, ok := s.byKey[key.String()]
	if !ok {
		return "", fmt.Errorf("no content for %s", key)
	}
	f, err := os.CreateTemp(s.dir, "sm-blob-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// TestV2ConcurrentFirstPushesAllLandInIndex pins the machines-index
// conditional-write retry under real contention: many machines pushing
// their first snapshot concurrently must all end up registered, with no
// lost updates. It exercises both drivers because the retry is depot
// behaviour, not a local-filesystem locking accident.
func TestV2ConcurrentFirstPushesAllLandInIndex(t *testing.T) {
	stores := []struct {
		name string
		open func(*testing.T) *depot.V2
	}{
		{name: "local", open: newLocalV2},
		{name: "r2", open: func(t *testing.T) *depot.V2 {
			f := newFakeS3(t)
			t.Cleanup(f.Close)
			v2 := depot.NewV2FromR2(f.Depot("bucket"))
			if err := v2.Init(t.Context()); err != nil {
				t.Fatal(err)
			}
			return v2
		}},
	}
	for _, store := range stores {
		t.Run(store.name, func(t *testing.T) {
			ctx := t.Context()
			v2 := store.open(t)
			const n = 8
			var wg sync.WaitGroup
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					machine := fmt.Sprintf("conc-%d", i)
					_, errs[i] = pushV2State(ctx, v2, machine, map[string]string{"s.jsonl": "content " + machine})
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("concurrent push %d: %v", i, err)
				}
			}
			machines, err := v2.Machines(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(machines) != n {
				t.Fatalf("machines index lost updates: %d/%d registered: %v", len(machines), n, machines)
			}
			report, err := v2.Verify(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Problems) != 0 {
				t.Fatalf("verify after concurrent pushes: %v", report.Problems)
			}
		})
	}
}
