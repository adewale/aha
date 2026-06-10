package cas_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cas"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func newStore(t *testing.T) *cas.Store {
	t.Helper()
	s, err := cas.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeSrc(t *testing.T, data []byte) (string, model.BlobKey) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := model.NewBlobKey(hash.SHA256Bytes(data))
	if err != nil {
		t.Fatal(err)
	}
	return p, key
}

// TestStoreRoundTrip pins the core contract: bytes stored under their
// content address come back identical and verified, across sizes
// including empty and multi-block.
func TestStoreRoundTrip(t *testing.T) {
	s := newStore(t)
	for _, size := range []int{0, 1, 100, 1 << 16, 1<<20 + 17} {
		data := make([]byte, size)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		src, key := writeSrc(t, data)
		created, err := s.PutFile(key, src)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !created {
			t.Fatalf("size %d: expected created", size)
		}
		rc, err := s.Open(key)
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
		if !bytes.Equal(got, data) {
			t.Fatalf("size %d: round trip mismatch", size)
		}
		ok, err := s.Has(key)
		if err != nil || !ok {
			t.Fatalf("Has=%v err=%v after put", ok, err)
		}
	}
}

// TestStorePutFileIsWriteOnce pins I1's write-once semantics: an existing
// blob is never rewritten (same address ⇒ same content by construction, so
// rewriting is pure waste and a corruption hazard).
func TestStorePutFileIsWriteOnce(t *testing.T) {
	s := newStore(t)
	data := []byte("blob bytes")
	src, key := writeSrc(t, data)
	if _, err := s.PutFile(key, src); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("sentinel-existing-blob")
	if err := os.WriteFile(s.Path(key), sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := s.PutFile(key, src)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("PutFile rewrote an existing blob")
	}
	got, err := os.ReadFile(s.Path(key))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatal("existing blob bytes were replaced")
	}
}

// TestStorePutFileRejectsWrongKey pins that a blob cannot be stored under
// an address that is not the hash of its content, and that a rejected put
// leaves nothing behind (atomicity: failed writes are invisible).
func TestStorePutFileRejectsWrongKey(t *testing.T) {
	s := newStore(t)
	src, _ := writeSrc(t, []byte("actual content"))
	wrongKey, err := model.NewBlobKey(strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutFile(wrongKey, src); err == nil {
		t.Fatal("PutFile stored content under a non-matching address")
	}
	if _, err := os.Stat(s.Path(wrongKey)); !os.IsNotExist(err) {
		t.Fatalf("rejected put left a blob behind: %v", err)
	}
	entries, err := os.ReadDir(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Fatalf("rejected put left litter: %s", e.Name())
	}
}

// TestStoreOpenVerifiesContent pins read-side verification (I1 residual
// risk): a corrupted or truncated stored blob is detected before its bytes
// are trusted, not passed through silently.
func TestStoreOpenVerifiesContent(t *testing.T) {
	s := newStore(t)
	data := bytes.Repeat([]byte("session line\n"), 4096)
	src, key := writeSrc(t, data)
	if _, err := s.PutFile(key, src); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(s.Path(key))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := func(name string, mutate func([]byte) []byte) {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(s.Path(key), mutate(append([]byte(nil), stored...)), 0o644); err != nil {
				t.Fatal(err)
			}
			rc, err := s.Open(key)
			if err == nil {
				_, err = io.ReadAll(rc)
				if cerr := rc.Close(); err == nil {
					err = cerr
				}
			}
			if err == nil {
				t.Fatalf("%s blob was read without an error", name)
			}
		})
	}
	corrupt("bit-flipped", func(b []byte) []byte { b[len(b)/2] ^= 0x01; return b })
	corrupt("truncated", func(b []byte) []byte { return b[:len(b)/2] })
	corrupt("swapped-content", func(b []byte) []byte {
		other := []byte("entirely different content")
		osrc, okey := writeSrc(t, other)
		if _, err := s.PutFile(okey, osrc); err != nil {
			t.Fatal(err)
		}
		swapped, err := os.ReadFile(s.Path(okey))
		if err != nil {
			t.Fatal(err)
		}
		return swapped
	})
}

func TestStoreOpenMissingBlob(t *testing.T) {
	s := newStore(t)
	key, err := model.NewBlobKey(strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(key); err == nil {
		t.Fatal("Open succeeded for a missing blob")
	}
	ok, err := s.Has(key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("Has reported a missing blob as present")
	}
}
