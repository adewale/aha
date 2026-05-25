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

type AtomicOptions struct {
	TempPattern string
	ExistingOK  bool
}

func AtomicWrite(path string, opts AtomicOptions, write func(*os.File) error) error {
	_, err := AtomicWriteCreated(path, opts, write)
	return err
}

func AtomicWriteCreated(path string, opts AtomicOptions, write func(*os.File) error) (bool, error) {
	if opts.ExistingOK {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	pattern := opts.TempPattern
	if pattern == "" {
		pattern = ".tmp-*"
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	writeErr := write(tmp)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return false, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return false, closeErr
	}
	if opts.ExistingOK {
		if _, err := os.Stat(path); err == nil {
			_ = os.Remove(tmpPath)
			return false, nil
		} else if !os.IsNotExist(err) {
			_ = os.Remove(tmpPath)
			return false, err
		}
		if err := os.Link(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			if os.IsExist(err) {
				return false, nil
			}
			return false, err
		}
		_ = os.Remove(tmpPath)
		return true, nil
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return false, err
	}
	return true, nil
}

func AtomicWriteBytes(path string, data []byte, opts AtomicOptions) error {
	return AtomicWrite(path, opts, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

func AtomicCopyReader(path string, src io.Reader, opts AtomicOptions) error {
	return AtomicWrite(path, opts, func(f *os.File) error {
		_, err := io.Copy(f, src)
		return err
	})
}

func AtomicCopyFile(path, srcPath string, opts AtomicOptions) error {
	_, err := AtomicCopyFileCreated(path, srcPath, opts)
	return err
}

func AtomicCopyFileCreated(path, srcPath string, opts AtomicOptions) (bool, error) {
	if opts.ExistingOK {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return false, err
	}
	defer in.Close()
	return AtomicWriteCreated(path, opts, func(f *os.File) error {
		_, err := io.Copy(f, in)
		return err
	})
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
