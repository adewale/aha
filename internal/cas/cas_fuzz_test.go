package cas_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cas"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/klauspost/compress/zstd"
)

// FuzzVerifyReader feeds arbitrary bytes to the blob reader as if they
// were a stored object: it must never panic, and it may succeed ONLY for
// inputs that an independent zstd decompression confirms carry exactly
// the keyed content (zstd has many valid encodings of one plaintext, so
// the oracle checks the decompressed bytes, not the compressed framing).
// Any other success would mean corrupted storage could pass content
// verification.
func FuzzVerifyReader(f *testing.F) {
	key, err := model.NewBlobKey(hash.SHA256Bytes([]byte("the one true content")))
	if err != nil {
		f.Fatal(err)
	}
	var legit bytes.Buffer
	enc, err := zstd.NewWriter(&legit)
	if err != nil {
		f.Fatal(err)
	}
	if _, err := enc.Write([]byte("the one true content")); err != nil {
		f.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		f.Fatal(err)
	}
	truth := legit.Bytes()
	f.Add(truth)                                  // the only verifiable input
	f.Add(truth[:len(truth)/2])                   // truncated
	f.Add([]byte{})                               // empty
	f.Add([]byte(strings.Repeat("garbage", 100))) // not zstd
	f.Fuzz(func(t *testing.T, data []byte) {
		rc, err := cas.VerifyReader(io.NopCloser(bytes.NewReader(data)), key)
		if err != nil {
			return
		}
		_, err = io.Copy(io.Discard, rc)
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return
		}
		dec, derr := zstd.NewReader(bytes.NewReader(data))
		if derr != nil {
			t.Fatalf("VerifyReader accepted bytes an independent decoder rejects: %v", derr)
		}
		plain, derr := io.ReadAll(dec)
		dec.Close()
		if derr != nil {
			t.Fatalf("VerifyReader accepted bytes an independent decode cannot read: %v", derr)
		}
		if hash.SHA256Bytes(plain) != key.String() {
			t.Fatalf("VerifyReader accepted content hashing to %s, key is %s", hash.SHA256Bytes(plain), key)
		}
	})
}
