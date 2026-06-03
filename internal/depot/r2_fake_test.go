package depot_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestR2VerifyQuickDeepAndRepairOperationBudgets(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, _, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	fake.resetCounts()
	report, err := depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Deep {
		t.Fatalf("quick verify reported deep: %+v", report)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("quick verify problems: %+v", report.Problems)
	}
	if got := fake.count(http.MethodGet, ref.Key); got != 0 {
		t.Fatalf("quick verify downloaded bundle %s %d times", ref.Key, got)
	}
	if got := fake.byteCount(http.MethodGet, ref.Key); got != 0 {
		t.Fatalf("quick verify downloaded bundle bytes=%d", got)
	}
	if got := fake.count(http.MethodHead, ref.Key); got != 1 {
		t.Fatalf("quick verify head bundle count=%d, want 1", got)
	}
	if got := fake.count("LIST", "catalog/v1/"); got != 1 {
		t.Fatalf("quick verify catalog list count=%d, want 1", got)
	}
	fake.resetCounts()
	report, err = depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{Deep: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Deep {
		t.Fatalf("deep verify did not report deep: %+v", report)
	}
	if got := fake.count(http.MethodGet, ref.Key); got != 1 {
		t.Fatalf("deep verify bundle GET count=%d, want 1", got)
	}
	if got := fake.byteCount(http.MethodGet, ref.Key); got == 0 {
		t.Fatalf("deep verify did not download bundle bytes")
	}
	if got := fake.count("LIST", "bundles/v1/"); got != 1 {
		t.Fatalf("deep verify bundle list count=%d, want 1", got)
	}
	fake.resetCounts()
	report, err = depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{Repair: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Deep || !report.Repaired {
		t.Fatalf("repair did not report deep+repaired: %+v", report)
	}
	if got := fake.count(http.MethodGet, ref.Key); got != 1 {
		t.Fatalf("repair bundle GET count=%d, want 1", got)
	}
	if got := fake.byteCount(http.MethodGet, ref.Key); got == 0 {
		t.Fatalf("repair did not download bundle bytes")
	}
	if got := fake.count("LIST", "catalog/v1/"); got < 2 {
		t.Fatalf("repair catalog list count=%d, want at least verify+delete listings", got)
	}
	if got := fake.count(http.MethodDelete, "catalog/v1/m1.json"); got != 1 {
		t.Fatalf("repair catalog delete count=%d, want 1", got)
	}
	if got := fake.count(http.MethodPut, "catalog/v1/m1.json"); got != 1 {
		t.Fatalf("repair catalog put count=%d, want 1", got)
	}
}

func TestR2QuickVerifyReportsOrphanBundleWithoutMarker(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, err := depot.BundleRefFromPath(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	fake.put(ref.Key, b)
	fake.mu.Lock()
	delete(fake.objects, "depot.json")
	delete(fake.etags, "depot.json")
	fake.mu.Unlock()
	fake.resetCounts()
	report, err := depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Deep {
		t.Fatalf("quick verify reported deep: %+v", report)
	}
	if report.Bundles != 1 {
		t.Fatalf("quick verify should count raw bundle objects, got %+v", report)
	}
	problems := strings.Join(report.Problems, "\n")
	if !strings.Contains(problems, "missing depot marker") || !strings.Contains(problems, "catalog missing bundle "+ref.BundleSHA256) {
		t.Fatalf("quick verify did not report degraded orphan bundle: %+v", report)
	}
	if got := fake.count(http.MethodGet, ref.Key); got != 0 {
		t.Fatalf("quick verify downloaded orphan bundle count=%d", got)
	}
}

func TestR2QuickVerifySurfacesMarkerReadErrors(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.failGet = map[string]int{"depot.json": http.StatusInternalServerError}
	_, err := depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{})
	if err == nil {
		t.Fatal("quick verify swallowed marker read server error")
	}
}

func TestR2QuickVerifySurfacesHeadObjectErrors(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("e", 64)
	shard := depot.CatalogShard{Schema: depot.CatalogSchema, MachineID: "m1", Bundles: []depot.BundleRef{{BundleSHA256: sha, MachineID: "m1", Key: depot.BundleKey(sha)}}}
	b, _ := json.Marshal(shard)
	fake.put("catalog/v1/m1.json", b)
	fake.failHead = map[string]int{depot.BundleKey(sha): http.StatusInternalServerError}
	_, err := depot.VerifyWithOptions(t.Context(), d, depot.VerifyOptions{})
	if err == nil {
		t.Fatal("quick verify swallowed HeadObject server error")
	}
}

func TestR2ListOperationBudgetDoesNotTouchBundles(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundlePath := writeDepotTestBundle(t, filepath.Join(t.TempDir(), "src"))
	ref, _, err := d.PutBundle(t.Context(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	fake.resetCounts()
	refs, err := d.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs=%d, want 1", len(refs))
	}
	if got := fake.count("LIST", "catalog/v1/"); got != 1 {
		t.Fatalf("list catalog LIST count=%d, want 1", got)
	}
	if got := fake.count(http.MethodGet, "catalog/v1/m1.json"); got != 1 {
		t.Fatalf("list catalog GET count=%d, want 1", got)
	}
	if got := fake.count(http.MethodGet, ref.Key); got != 0 {
		t.Fatalf("list downloaded bundle count=%d, want 0", got)
	}
}

func TestR2CompactOperationBudgetAndDedupe(t *testing.T) {
	fake := newFakeS3(t)
	defer fake.Close()
	d := fake.Depot("bucket")
	if err := d.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("c", 64)
	shard := depot.CatalogShard{Schema: depot.CatalogSchema, MachineID: "m1", Bundles: []depot.BundleRef{{BundleSHA256: sha, MachineID: "m1", Key: depot.BundleKey(sha)}, {BundleSHA256: sha, MachineID: "m1", Key: depot.BundleKey(sha)}}}
	b, _ := json.Marshal(shard)
	fake.put("catalog/v1/m1.json", b)
	fake.resetCounts()
	report, err := depot.Compact(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if report.RefsBefore != 2 || report.RefsAfter != 1 || report.DuplicateRefs != 1 {
		t.Fatalf("compact report: %+v", report)
	}
	if got := fake.count("LIST", "catalog/v1/"); got != 1 {
		t.Fatalf("compact catalog LIST count=%d, want 1", got)
	}
	if got := fake.count(http.MethodPut, "catalog/v1/m1.json"); got != 1 {
		t.Fatalf("compact catalog PUT count=%d, want 1", got)
	}
	if got := fake.count(http.MethodGet, depot.BundleKey(sha)); got != 0 {
		t.Fatalf("compact touched bundle bytes: GET count=%d", got)
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
	t          *testing.T
	server     *httptest.Server
	mu         sync.Mutex
	objects    map[string][]byte
	etags      map[string]string
	counts     map[string]int
	byteCounts map[string]int64
	failHead   map[string]int
	failGet    map[string]int
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{t: t, objects: map[string][]byte{}, etags: map[string]string{}, counts: map[string]int{}, byteCounts: map[string]int64{}}
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

func (f *fakeS3) resetCounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = map[string]int{}
	f.byteCounts = map[string]int64{}
}

func (f *fakeS3) count(method, key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[method+" "+key]
}

func (f *fakeS3) byteCount(method, key string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byteCounts[method+" "+key]
}

func (f *fakeS3) record(method, key string, n int64) {
	f.counts[method+" "+key]++
	f.byteCounts[method+" "+key] += n
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
		prefix := r.URL.Query().Get("prefix")
		f.mu.Lock()
		f.record("LIST", prefix, 0)
		f.mu.Unlock()
		f.writeList(w, prefix)
		return
	}
	f.mu.Lock()
	f.record(r.Method, key, 0)
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodHead:
		if code := f.failHead[key]; code != 0 {
			w.WriteHeader(code)
			return
		}
		if _, ok := f.objects[key]; !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", f.etags[key])
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if code := f.failGet[key]; code != 0 {
			w.WriteHeader(code)
			return
		}
		b, ok := f.objects[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.byteCounts[r.Method+" "+key] += int64(len(b))
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
		f.byteCounts[r.Method+" "+key] += int64(len(b))
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
