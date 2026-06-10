// Package cas is the shared content-addressed store of zstd-compressed
// file versions, used by the corpus blob store and the depot v2 blob
// layout (docs/depot-v2-spec.md, invariant I1). A blob's key IS the
// SHA-256 of its uncompressed content:
//
//   - PutFile verifies the content hashes to the key while compressing,
//     so a blob stored under the wrong address is unrepresentable;
//   - writes are atomic (temp + rename) and write-once (an existing blob
//     is never rewritten), so identical content is one object and a
//     failed write is invisible;
//   - Open verifies the decompressed content against the key before EOF,
//     so corruption is detected before bytes are trusted (the residual
//     risk Go cannot prevent at construction time).
package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdhash "hash"
	"io"
	"os"
	"sync"

	"github.com/adewale/aha/internal/fileutil"
	"github.com/adewale/aha/internal/model"
	"github.com/klauspost/compress/zstd"
)

// Store is a content-addressed blob directory. Blobs live directly under
// the root as <sha256>.zst.
type Store struct{ root string }

// Open creates the root directory if needed and returns the store.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Root returns the store's root directory.
func (s *Store) Root() string { return s.root }

// Path returns the filesystem path a blob is (or would be) stored at.
func (s *Store) Path(key model.BlobKey) string {
	return s.root + string(os.PathSeparator) + key.String() + ".zst"
}

// Has reports whether the blob exists.
func (s *Store) Has(key model.BlobKey) (bool, error) {
	_, err := os.Stat(s.Path(key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// PutFile stores the file at srcPath under key, compressing it and
// verifying that the content's SHA-256 equals the key. It reports whether
// a new blob was created; an existing blob is left untouched.
func (s *Store) PutFile(key model.BlobKey, srcPath string) (bool, error) {
	if !key.Valid() {
		return false, fmt.Errorf("cas: invalid blob key")
	}
	return fileutil.AtomicWriteCreated(s.Path(key), fileutil.AtomicOptions{TempPattern: key.String() + "-*.tmp", ExistingOK: true}, func(out *os.File) error {
		in, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer in.Close()
		enc, err := pooledEncoder(out)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(enc, io.TeeReader(in, h))
		closeErr := enc.Close()
		if closeErr == nil {
			putEncoder(enc)
		}
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != key.String() {
			return fmt.Errorf("cas: content hashes to %s, not blob key %s", got, key)
		}
		return nil
	})
}

// Open returns a verified reader over the blob's uncompressed content.
// Reads fail with an error before or at EOF if the stored bytes do not
// decompress to content matching the key.
func (s *Store) Open(key model.BlobKey) (io.ReadCloser, error) {
	f, err := os.Open(s.Path(key))
	if err != nil {
		return nil, err
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &verifyingReader{key: key, f: f, zr: zr, h: sha256.New()}, nil
}

type verifyingReader struct {
	key      model.BlobKey
	f        *os.File
	zr       *zstd.Decoder
	h        stdhash.Hash
	verified bool
}

func (r *verifyingReader) Read(p []byte) (int, error) {
	n, err := r.zr.Read(p)
	if n > 0 {
		_, _ = r.h.Write(p[:n])
	}
	if err == io.EOF {
		if got := hex.EncodeToString(r.h.Sum(nil)); got != r.key.String() {
			return n, fmt.Errorf("cas: blob %s content verification failed (got %s)", r.key, got)
		}
		r.verified = true
	}
	return n, err
}

func (r *verifyingReader) Close() error {
	r.zr.Close()
	return r.f.Close()
}

var encoderPool sync.Pool

func pooledEncoder(w io.Writer) (*zstd.Encoder, error) {
	if v := encoderPool.Get(); v != nil {
		enc := v.(*zstd.Encoder)
		enc.Reset(w)
		return enc, nil
	}
	return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
}

func putEncoder(enc *zstd.Encoder) {
	enc.Reset(nil)
	encoderPool.Put(enc)
}
