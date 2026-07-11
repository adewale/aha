package depot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// bucketStoreAgainst builds an r2StoreV2 whose client talks to handler.
// White-box (package depot): ensureBucket is the unexported seam between
// `aha depot init` and the S3 CreateBucket permission model.
func bucketStoreAgainst(t *testing.T, handler http.HandlerFunc) *r2StoreV2 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("key-canary", "secret-canary", ""), Retryer: aws.NopRetryer{}})
	return &r2StoreV2{bucket: "smoke-bucket", client: client}
}

// A HeadBucket failure that is not NotFound (bad credentials, wrong account,
// wrong endpoint) must surface as itself. The old code fell through to
// CreateBucket, which either masked the real error with a CreateBucket
// denial or — worse — silently "succeeded" against a permissive endpoint.
func TestEnsureBucketSurfacesHeadErrorsWithoutCreating(t *testing.T) {
	var createAttempts atomic.Int32
	store := bucketStoreAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusForbidden)
		case http.MethodPut:
			createAttempts.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	err := store.ensureBucket(context.Background())
	if err == nil {
		t.Fatal("HeadBucket 403 must surface, not vanish behind CreateBucket")
	}
	if got := createAttempts.Load(); got != 0 {
		t.Fatalf("CreateBucket attempted %d times after a non-NotFound HeadBucket error", got)
	}
	message := err.Error()
	for _, want := range []string{"403", "HeadBucket", "before any depot mutation", "Object Read & Write", "matching access key and secret", "smoke-bucket"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing actionable detail %q", message, want)
		}
	}
	if strings.Contains(message, "secret-canary") {
		t.Fatalf("error leaked credentials: %q", message)
	}
}

// CreateBucket denial is the expected failure for the recommended
// Object Read & Write token (only Admin tokens may create buckets), so the
// error must name the bucket-creation step — that is what the CLI hint
// layer keys on to stop telling users to "fix" a token that is already
// correctly scoped.
func TestEnsureBucketCreateDenialNamesBucketCreation(t *testing.T) {
	store := bucketStoreAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	err := store.ensureBucket(context.Background())
	if err == nil {
		t.Fatal("expected CreateBucket denial to surface")
	}
	if !strings.Contains(err.Error(), `create r2 bucket "smoke-bucket"`) {
		t.Fatalf("error does not name the bucket-creation step: %v", err)
	}
}

// The happy auto-create path: missing bucket plus an Admin token creates
// the bucket exactly once and succeeds.
func TestEnsureBucketCreatesMissingBucket(t *testing.T) {
	var createAttempts atomic.Int32
	store := bucketStoreAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			createAttempts.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	if err := store.ensureBucket(context.Background()); err != nil {
		t.Fatalf("auto-create failed: %v", err)
	}
	if got := createAttempts.Load(); got != 1 {
		t.Fatalf("CreateBucket attempted %d times, want exactly 1", got)
	}
}
