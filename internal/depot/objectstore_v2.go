package depot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/fileutil"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// objectStore is the complete primitive surface available to depot v2
// steady-state paths (push/pull/status). Its shape carries three
// invariants from docs/depot-v2-spec.md by construction:
//
//   - I5: there is no delete primitive, so v2 code cannot delete;
//   - I6: there is no list primitive, so push/pull cannot LIST — listing
//     exists only on objectLister, which only Verify may use;
//   - writes are either write-once (putIfAbsent) or conditional
//     (putConditional), so blind overwrites are inexpressible.
type objectStore interface {
	// get returns the object bytes and an opaque etag for conditional
	// writes. Missing objects return errObjectNotExist.
	get(ctx context.Context, key string) ([]byte, string, error)
	// getStream returns the object content as a stream (for blobs).
	getStream(ctx context.Context, key string) (io.ReadCloser, error)
	// putFileIfAbsent writes the file at srcPath to key unless the key
	// already exists. Reports whether a new object was created.
	putFileIfAbsent(ctx context.Context, key, contentType, srcPath string) (bool, error)
	// putBytesIfAbsent writes b to key unless the key already exists.
	putBytesIfAbsent(ctx context.Context, key, contentType string, b []byte) (bool, error)
	// putBytesConditional writes b to key if the current object matches
	// etag (etag "" requires the key to be absent). A lost race returns
	// errPreconditionFailed.
	putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error
	// exists reports whether key is present.
	exists(ctx context.Context, key string) (bool, error)
}

// objectLister is the audit-only listing surface. Only Verify may use it
// (invariant I6); steady-state code receives an objectStore and therefore
// cannot list.
type objectLister interface {
	listKeys(ctx context.Context, prefix string) ([]string, error)
}

var (
	errObjectNotExist     = errors.New("object does not exist")
	errPreconditionFailed = errors.New("precondition failed")
)

// ---- local filesystem store ------------------------------------------

type localStoreV2 struct{ root string }

func (s *localStoreV2) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func localEtag(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *localStoreV2) get(ctx context.Context, key string) ([]byte, string, error) {
	b, err := os.ReadFile(s.path(key))
	if os.IsNotExist(err) {
		return nil, "", fmt.Errorf("%w: %s", errObjectNotExist, key)
	}
	if err != nil {
		return nil, "", err
	}
	return b, localEtag(b), nil
}

func (s *localStoreV2) getStream(ctx context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(s.path(key))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", errObjectNotExist, key)
	}
	return f, err
}

func (s *localStoreV2) putFileIfAbsent(ctx context.Context, key, contentType, srcPath string) (bool, error) {
	return fileutil.AtomicCopyFileCreated(s.path(key), srcPath, fileutil.AtomicOptions{TempPattern: ".tmp-*.obj", ExistingOK: true})
}

func (s *localStoreV2) putBytesIfAbsent(ctx context.Context, key, contentType string, b []byte) (bool, error) {
	return fileutil.AtomicWriteCreated(s.path(key), fileutil.AtomicOptions{TempPattern: ".tmp-*.obj", ExistingOK: true}, func(out *os.File) error {
		_, err := out.Write(b)
		return err
	})
}

func (s *localStoreV2) putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error {
	return withLocalLock(s.root, func() error {
		current, err := os.ReadFile(s.path(key))
		switch {
		case os.IsNotExist(err):
			if etag != "" {
				return fmt.Errorf("%w: %s was removed", errPreconditionFailed, key)
			}
		case err != nil:
			return err
		default:
			if etag == "" || localEtag(current) != etag {
				return fmt.Errorf("%w: %s changed", errPreconditionFailed, key)
			}
		}
		return fileutil.AtomicWriteBytes(s.path(key), b, fileutil.AtomicOptions{TempPattern: ".tmp-*.obj"})
	})
}

func (s *localStoreV2) exists(ctx context.Context, key string) (bool, error) {
	if _, err := os.Stat(s.path(key)); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func (s *localStoreV2) listKeys(ctx context.Context, prefix string) ([]string, error) {
	root := s.path(prefix)
	var keys []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	return keys, err
}

// ---- R2 / S3 store -----------------------------------------------------

type r2StoreV2 struct {
	bucket string
	client *s3.Client
}

func (s *r2StoreV2) get(ctx context.Context, key string) ([]byte, string, error) {
	obj, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isS3NotFound(err) {
			return nil, "", fmt.Errorf("%w: %s", errObjectNotExist, key)
		}
		return nil, "", err
	}
	defer obj.Body.Close()
	b, err := io.ReadAll(obj.Body)
	if err != nil {
		return nil, "", err
	}
	etag := ""
	if obj.ETag != nil {
		etag = *obj.ETag
	}
	return b, etag, nil
}

func (s *r2StoreV2) getStream(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isS3NotFound(err) {
			return nil, fmt.Errorf("%w: %s", errObjectNotExist, key)
		}
		return nil, err
	}
	return obj.Body, nil
}

func (s *r2StoreV2) putFileIfAbsent(ctx context.Context, key, contentType, srcPath string) (bool, error) {
	exists, err := s.exists(ctx, key)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: f, ContentType: aws.String(contentType), IfNoneMatch: aws.String("*")})
	if isS3PreconditionFailed(err) {
		return false, nil // lost a benign race: identical content-addressed object
	}
	return err == nil, err
}

func (s *r2StoreV2) putBytesIfAbsent(ctx context.Context, key, contentType string, b []byte) (bool, error) {
	exists, err := s.exists(ctx, key)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(b), ContentType: aws.String(contentType), IfNoneMatch: aws.String("*")})
	if isS3PreconditionFailed(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *r2StoreV2) putBytesConditional(ctx context.Context, key, contentType string, b []byte, etag string) error {
	input := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(b), ContentType: aws.String(contentType)}
	if etag == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(etag)
	}
	_, err := s.client.PutObject(ctx, input)
	if isS3PreconditionFailed(err) {
		return fmt.Errorf("%w: %s", errPreconditionFailed, key)
	}
	return err
}

func (s *r2StoreV2) exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err == nil {
		return true, nil
	}
	if isS3NotFound(err) {
		return false, nil
	}
	return false, err
}

func (s *r2StoreV2) listKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
	}
	return keys, nil
}

func (s *r2StoreV2) ensureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
		if _, createErr := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)}); createErr != nil {
			return createErr
		}
	}
	return nil
}

func isS3PreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "PreconditionFailed", "412":
		return true
	default:
		return strings.Contains(apiErr.ErrorCode(), "Precondition")
	}
}
