package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type CopyResult struct {
	SHA256 string
	Bytes  int64
}

func CopyHash(dst string, src io.Reader, maxBytes int64) (CopyResult, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return CopyResult{}, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return CopyResult{}, err
	}
	h := sha256.New()
	reader := src
	if maxBytes >= 0 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	n, copyErr := io.Copy(io.MultiWriter(out, h), reader)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return CopyResult{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return CopyResult{}, closeErr
	}
	if maxBytes >= 0 && n > maxBytes {
		_ = os.Remove(dst)
		return CopyResult{}, fmt.Errorf("input too large: exceeds %d bytes", maxBytes)
	}
	return CopyResult{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}

func HashDiscard(src io.Reader) (CopyResult, error) {
	h := sha256.New()
	n, err := io.Copy(h, src)
	if err != nil {
		return CopyResult{}, err
	}
	return CopyResult{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}
