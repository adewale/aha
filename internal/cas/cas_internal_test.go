package cas

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

// TestVerifyReaderCapsDecompressedBytes pins the zstd-bomb guard: a blob
// that decompresses past the budget is reported as an error mid-stream
// instead of being allowed to fill memory or disk. (White-box: the
// production cap is 4 GiB; the test shrinks it to keep the fixture tiny.)
func TestVerifyReaderCapsDecompressedBytes(t *testing.T) {
	old := maxDecompressedBlobBytes
	maxDecompressedBlobBytes = 1 << 10
	t.Cleanup(func() { maxDecompressedBlobBytes = old })

	dir := t.TempDir()
	data := bytes.Repeat([]byte{0}, 4<<10) // 4 KiB of zeros: tiny compressed, over the 1 KiB cap
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := model.NewBlobKey(hash.SHA256Bytes(data))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutFile(key, src); err != nil {
		t.Fatal(err)
	}
	rc, err := store.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		t.Fatal("blob decompressing past the budget was read without error")
	}
}
