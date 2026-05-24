package depot_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/depot"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestR2DepotPutListFetchVerifyRepairWithFakeS3(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	markerBefore := fake.get("depot.json")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if markerAfter := fake.get("depot.json"); string(markerAfter) != string(markerBefore) {
		t.Fatal("R2 init rewrote stable depot marker")
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, created, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !created || ref.Key != depot.BundleKey(ref.BundleSHA256) {
		t.Fatalf("bad R2 put created=%v ref=%+v", created, ref)
	}
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("bad R2 list: %+v", refs)
	}
	fetched := filepath.Join(t.TempDir(), "fetched.tar.zst")
	if err := d.Fetch(t.Context(), ref, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ReadManifest(fetched); err != nil {
		t.Fatalf("fetched bundle invalid: %v", err)
	}
	missingSHA := strings.Repeat("b", 64)
	stale := depot.CatalogShard{Schema: depot.CatalogSchema, MachineID: "stale", Bundles: []depot.BundleRef{{BundleSHA256: missingSHA, MachineID: "stale", Key: depot.BundleKey(missingSHA)}}}
	b, _ := json.Marshal(stale)
	fake.put("catalog/v1/stale.json", b)
	report, err := d.Verify(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Problems, "\n"), "catalog references missing bundle "+missingSHA) {
		t.Fatalf("verify did not report stale catalog ref: %+v", report)
	}
	report, err = d.Verify(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatalf("repair report not marked repaired: %+v", report)
	}
	refs, err = d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].BundleSHA256 != ref.BundleSHA256 {
		t.Fatalf("repair did not prune stale catalog shard: %+v", refs)
	}
}

func TestR2InitRejectsInvalidMarkerWithFakeS3(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	fake.put("depot.json", []byte(`{"schema":"wrong","layout":"v1"}`))
	if err := fake.Depot("bucket").Init(t.Context()); err == nil {
		t.Fatal("expected invalid marker error")
	}
}

type fakeS3 struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
	etags   map[string]string
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{t: t, objects: map[string][]byte{}, etags: map[string]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeS3) Close() { f.server.Close() }

func (f *fakeS3) Depot(bucket string) *depot.R2 {
	client := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(f.server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("key", "secret", "")})
	return &depot.R2{Bucket: bucket, Client: client, Config: depot.R2Config{Endpoint: f.server.URL, Region: "auto", AccessKeyID: "key", SecretAccessKey: "secret"}}
}

func (f *fakeS3) put(key string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = append([]byte(nil), b...)
	f.etags[key] = etag(b)
}

func (f *fakeS3) get(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.objects[key]...)
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	if r.Method == http.MethodHead && key == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodPut && key == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2" {
		f.writeList(w, r.URL.Query().Get("prefix"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodHead:
		if _, ok := f.objects[key]; !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", f.etags[key])
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", f.etags[key])
		_, _ = w.Write(b)
	case http.MethodPut:
		if r.Header.Get("If-None-Match") == "*" {
			if _, ok := f.objects[key]; ok {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
		}
		if want := r.Header.Get("If-Match"); want != "" && f.etags[key] != want {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.objects[key] = b
		f.etags[key] = etag(b)
		w.Header().Set("ETag", f.etags[key])
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		delete(f.objects, key)
		delete(f.etags, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3) writeList(w http.ResponseWriter, prefix string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := listBucketResult{Name: "bucket", Prefix: prefix, KeyCount: len(keys), MaxKeys: 1000, IsTruncated: false}
	for _, key := range keys {
		result.Contents = append(result.Contents, listContent{Key: key, LastModified: "2026-01-01T00:00:00Z", ETag: f.etags[key], Size: len(f.objects[key]), StorageClass: "STANDARD"})
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(result)
}

type listBucketResult struct {
	XMLName     xml.Name      `xml:"ListBucketResult"`
	Xmlns       string        `xml:"xmlns,attr,omitempty"`
	Name        string        `xml:"Name"`
	Prefix      string        `xml:"Prefix"`
	KeyCount    int           `xml:"KeyCount"`
	MaxKeys     int           `xml:"MaxKeys"`
	IsTruncated bool          `xml:"IsTruncated"`
	Contents    []listContent `xml:"Contents"`
}

type listContent struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int    `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

func etag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
