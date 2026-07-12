package depot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adewale/aha/internal/cas"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/model"
	ahaprogress "github.com/adewale/aha/internal/progress"
)

// V2 is a depot in the v2 content-addressed layout
// (docs/depot-v2-spec.md): file-version blobs stored once under their
// content address, snapshot manifests in per-machine namespaces, and a
// per-machine latest pointer. Steady-state methods use only the
// objectStore primitives, which expose no delete and no list (I5, I6).
type V2 struct {
	addr  Address
	store objectStore
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

const (
	conditionalRetryAttempts  = 12
	conditionalRetryBaseDelay = 5 * time.Millisecond
	conditionalRetryMaxDelay  = 250 * time.Millisecond
)

// ContentionError means a conditional object update kept losing races after
// the bounded retry policy. It is typed so callers can distinguish contention
// from corruption, credentials, and transport failures.
type LegacyArchiveError struct{}

func (*LegacyArchiveError) Error() string {
	return "Archive uses the unsupported v1 layout"
}

type ContentionError struct {
	Key      string
	Attempts int
}

func (e *ContentionError) Error() string {
	return fmt.Sprintf("conditional update contention for %s after %d attempts", e.Key, e.Attempts)
}

// waitForConditionalRetry backs off after a lost conditional write. The
// bounded, jittered delay prevents a herd of writers from immediately
// repeating the same GET/PUT race; context cancellation remains the bound on
// how long a caller is willing to wait for finite contention to converge.
func waitForConditionalRetry(ctx context.Context, attempt int) error {
	shift := attempt
	if shift > 6 { // 5ms << 6 exceeds the 250ms cap.
		shift = 6
	}
	upper := conditionalRetryBaseDelay << shift
	if upper > conditionalRetryMaxDelay {
		upper = conditionalRetryMaxDelay
	}
	lower := upper / 2
	delay := lower + time.Duration(rand.Int64N(int64(upper-lower)+1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NewLocalV2 opens a local-filesystem depot v2 rooted at root.
func NewLocalV2(root string) (*V2, error) {
	expanded, err := expandLocalRoot(root)
	if err != nil {
		return nil, err
	}
	return &V2{addr: Address{Type: "local", Location: expanded}, store: &localStoreV2{root: expanded}}, nil
}

// NewV2FromR2 opens a depot v2 over an existing R2 driver's bucket+client.
func NewV2FromR2(r *R2) *V2 {
	return &V2{addr: r.Address(), store: &r2StoreV2{bucket: r.Bucket().String(), client: r.S3Client()}}
}

func (v *V2) Address() Address { return v.addr }

// Init provisions the depot marker. It refuses to initialize over a v1
// layout: the two layouts never silently mix, and there is no migration
// (pre-release decision; v1 bundles remain importable via `aha ingest`).
func (v *V2) Init(ctx context.Context) error {
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return err
	}
	b, _, err := v.store.get(ctx, MarkerObjectKey)
	if err == nil {
		return validateMarkerV2Bytes(b)
	}
	if !errors.Is(err, errObjectNotExist) {
		return err
	}
	mb, err := markerV2Bytes()
	if err != nil {
		return err
	}
	if err := v.store.putBytesConditional(ctx, MarkerObjectKey, "application/json", mb, ""); err != nil {
		if errors.Is(err, errPreconditionFailed) {
			b, _, getErr := v.store.get(ctx, MarkerObjectKey)
			if getErr != nil {
				return getErr
			}
			return validateMarkerV2Bytes(b)
		}
		return err
	}
	return nil
}

// PreparedPull is a read capability for an initialised depot whose marker and
// machine index were validated before any local corpus mutation. Its fields are
// private so pull code cannot manufacture the capability from an unchecked V2.
type PreparedPull struct {
	depot    *V2
	machines []string
	binding  model.ArchiveBinding
}

// PreparedUpload is an opaque write capability for an initialised Archive.
// The zero value cannot publish or expose an Archive identity.
type PreparedUpload struct {
	depot   *V2
	binding model.ArchiveBinding
	state   model.ArchiveState
}

// DownloadPlan freezes the Archive latest vector before any Workspace is
// opened. Effectful download code accepts this opaque plan, not raw addresses.
type DownloadPlan struct {
	reader PreparedPull
	latest map[string]model.ManifestSHA256
	cache  *downloadManifestCache
}

type downloadManifestCacheKey struct {
	machine string
	sha     model.ManifestSHA256
}

type downloadManifestCache struct {
	mu        sync.Mutex
	manifests map[downloadManifestCacheKey]model.SnapshotManifest
}

// PreparePull performs the complete metadata preflight required before a
// caller creates or opens a writable corpus.
func (v *V2) PreparePull(ctx context.Context) (PreparedPull, error) {
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return PreparedPull{}, err
	}
	marker, _, err := v.store.get(ctx, MarkerObjectKey)
	if errors.Is(err, errObjectNotExist) {
		return PreparedPull{}, errors.New("Archive is not initialised")
	}
	if err != nil {
		return PreparedPull{}, err
	}
	binding, err := archiveBindingFromMarker(marker, v.addr)
	if err != nil {
		return PreparedPull{}, err
	}
	machines, err := v.Machines(ctx)
	if err != nil {
		return PreparedPull{}, err
	}
	return PreparedPull{depot: v, machines: append([]string(nil), machines...), binding: binding}, nil
}

func (v *V2) PrepareUpload(ctx context.Context) (PreparedUpload, error) {
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return PreparedUpload{}, err
	}
	markerBytes, _, err := v.store.get(ctx, MarkerObjectKey)
	if errors.Is(err, errObjectNotExist) {
		return PreparedUpload{}, errors.New("archive is not initialised")
	}
	if err != nil {
		return PreparedUpload{}, err
	}
	binding, err := archiveBindingFromMarker(markerBytes, v.addr)
	if err != nil {
		return PreparedUpload{}, err
	}
	machines, err := v.Machines(ctx)
	if err != nil {
		return PreparedUpload{}, err
	}
	state := model.ArchiveEmpty
	if len(machines) > 0 {
		state = model.ArchivePopulated
	}
	transition := model.ArchiveTransition(state, model.ArchiveUpload)
	if !transition.Allowed {
		return PreparedUpload{}, fmt.Errorf("Archive state %s does not allow upload", state)
	}
	return PreparedUpload{depot: v, binding: binding, state: state}, nil
}

func (v *V2) rejectLegacyArchive(ctx context.Context) error {
	if _, _, err := v.store.get(ctx, "depot.json"); err == nil {
		return &LegacyArchiveError{}
	} else if !errors.Is(err, errObjectNotExist) {
		return err
	}
	return nil
}

func archiveBindingFromMarker(data []byte, addr Address) (model.ArchiveBinding, error) {
	if err := validateMarkerV2Bytes(data); err != nil {
		return model.ArchiveBinding{}, err
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return model.ArchiveBinding{}, err
	}
	return model.NewArchiveBinding(m.DepotID, addr.String())
}

func (p PreparedPull) ArchiveBinding() (model.ArchiveBinding, error) {
	if p.depot == nil || !p.binding.Valid() {
		return model.ArchiveBinding{}, errors.New("invalid prepared Archive reader capability")
	}
	return p.binding, nil
}

func (p PreparedUpload) ArchiveBinding() (model.ArchiveBinding, error) {
	if p.depot == nil || !p.binding.Valid() {
		return model.ArchiveBinding{}, errors.New("invalid prepared Archive writer capability")
	}
	return p.binding, nil
}

func (p PreparedUpload) ArchiveState() (model.ArchiveState, error) {
	if p.depot == nil || !p.binding.Valid() || (p.state != model.ArchiveEmpty && p.state != model.ArchivePopulated) {
		return "", errors.New("invalid prepared Archive writer capability")
	}
	return p.state, nil
}

func (p PreparedPull) PlanDownload(ctx context.Context) (DownloadPlan, error) {
	if p.depot == nil || !p.binding.Valid() {
		return DownloadPlan{}, errors.New("invalid prepared Archive reader capability")
	}
	latest := make(map[string]model.ManifestSHA256, len(p.machines))
	for _, machine := range p.machines {
		sha, ok, err := p.Latest(ctx, machine)
		if err != nil {
			return DownloadPlan{}, err
		}
		if !ok {
			return DownloadPlan{}, fmt.Errorf("Archive machine %q has no latest snapshot", machine)
		}
		latest[machine] = sha
	}
	return DownloadPlan{reader: p, latest: latest, cache: &downloadManifestCache{manifests: map[downloadManifestCacheKey]model.SnapshotManifest{}}}, nil
}

func (p DownloadPlan) ArchiveBinding() (model.ArchiveBinding, error) {
	return p.reader.ArchiveBinding()
}

func (p DownloadPlan) LatestVector() (map[string]string, error) {
	if p.reader.depot == nil || p.latest == nil {
		return nil, errors.New("invalid Archive download plan")
	}
	out := make(map[string]string, len(p.latest))
	for machine, sha := range p.latest {
		out[machine] = sha.String()
	}
	return out, nil
}

func (p DownloadPlan) Machines() ([]string, error) {
	if p.reader.depot == nil || p.latest == nil {
		return nil, errors.New("invalid Archive download plan")
	}
	out := make([]string, 0, len(p.latest))
	for machine := range p.latest {
		out = append(out, machine)
	}
	slices.Sort(out)
	return out, nil
}

func (p DownloadPlan) Latest(machine string) (model.ManifestSHA256, bool, error) {
	if p.reader.depot == nil || p.latest == nil {
		return model.ManifestSHA256{}, false, errors.New("invalid Archive download plan")
	}
	sha, ok := p.latest[machine]
	return sha, ok, nil
}

func (p DownloadPlan) Manifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.SnapshotManifest, error) {
	if p.cache == nil {
		return model.SnapshotManifest{}, errors.New("invalid Archive download plan")
	}
	key := downloadManifestCacheKey{machine: machine, sha: sha}
	p.cache.mu.Lock()
	manifest, ok := p.cache.manifests[key]
	p.cache.mu.Unlock()
	if ok {
		return manifest, nil
	}
	manifest, err := p.reader.Manifest(ctx, machine, sha)
	if err != nil {
		return model.SnapshotManifest{}, err
	}
	p.cache.mu.Lock()
	p.cache.manifests[key] = manifest
	p.cache.mu.Unlock()
	return manifest, nil
}

func (p DownloadPlan) OpenBlob(ctx context.Context, key model.BlobKey) (io.ReadCloser, error) {
	return p.reader.OpenBlob(ctx, key)
}

type DownloadPlanSummary struct {
	Machines        int   `json:"machines"`
	LatestSnapshots int   `json:"latest_snapshots"`
	UnknownBlobs    int64 `json:"unknown_blobs"`
	UnknownBytes    int64 `json:"unknown_bytes"`
}

func (p DownloadPlan) Summary(ctx context.Context, known func(model.BlobKey) (bool, error)) (DownloadPlanSummary, error) {
	return p.SummaryDelta(ctx, nil, known)
}

// SummaryDelta examines manifests only for machines whose planned latest
// identity differs from the Workspace materialised vector.
func (p DownloadPlan) SummaryDelta(ctx context.Context, materialised map[string]string, known func(model.BlobKey) (bool, error)) (DownloadPlanSummary, error) {
	machines, err := p.Machines()
	if err != nil {
		return DownloadPlanSummary{}, err
	}
	if known == nil {
		known = func(model.BlobKey) (bool, error) { return false, nil }
	}
	summary := DownloadPlanSummary{Machines: len(machines), LatestSnapshots: len(p.latest)}
	seen := map[string]bool{}
	for _, machine := range machines {
		sha := p.latest[machine]
		if materialised[machine] == sha.String() {
			continue
		}
		manifest, err := p.Manifest(ctx, machine, sha)
		if err != nil {
			return DownloadPlanSummary{}, err
		}
		for _, file := range manifest.Files {
			if seen[file.SHA256] {
				continue
			}
			seen[file.SHA256] = true
			key, err := model.NewBlobKey(file.SHA256)
			if err != nil {
				return DownloadPlanSummary{}, err
			}
			present, err := known(key)
			if err != nil {
				return DownloadPlanSummary{}, err
			}
			if !present {
				summary.UnknownBlobs++
				summary.UnknownBytes += file.Bytes
			}
		}
	}
	return summary, nil
}

func (p PreparedPull) Machines() ([]string, error) {
	if p.depot == nil {
		return nil, errors.New("invalid prepared pull capability")
	}
	return append([]string(nil), p.machines...), nil
}

func (p PreparedPull) Latest(ctx context.Context, machine string) (model.ManifestSHA256, bool, error) {
	if p.depot == nil {
		return model.ManifestSHA256{}, false, errors.New("invalid prepared pull capability")
	}
	return p.depot.Latest(ctx, machine)
}

func (p PreparedPull) Manifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.SnapshotManifest, error) {
	if p.depot == nil {
		return model.SnapshotManifest{}, errors.New("invalid prepared pull capability")
	}
	return p.depot.Manifest(ctx, machine, sha)
}

func (p PreparedPull) OpenBlob(ctx context.Context, key model.BlobKey) (io.ReadCloser, error) {
	if p.depot == nil {
		return nil, errors.New("invalid prepared pull capability")
	}
	return p.depot.OpenBlob(ctx, key)
}

// Machines returns the registered machine namespaces (one GET, no LIST).
func (v *V2) Machines(ctx context.Context) ([]string, error) {
	b, _, err := v.store.get(ctx, MachinesIndexKey)
	if errors.Is(err, errObjectNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return DecodeMachinesIndex(b)
}

// Latest returns the machine's latest snapshot identity, if any.
func (v *V2) Latest(ctx context.Context, machine string) (model.ManifestSHA256, bool, error) {
	b, _, err := v.store.get(ctx, LatestPointerKey(machine))
	if errors.Is(err, errObjectNotExist) {
		return model.ManifestSHA256{}, false, nil
	}
	if err != nil {
		return model.ManifestSHA256{}, false, err
	}
	sha, err := DecodeLatestPointer(b)
	if err != nil {
		return model.ManifestSHA256{}, false, err
	}
	return sha, true, nil
}

// Manifest fetches and verifies one snapshot manifest: the bytes must be
// canonical, hash to the requested identity, and belong to the requested
// machine's namespace.
func (v *V2) Manifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.SnapshotManifest, error) {
	if !sha.Valid() {
		return model.SnapshotManifest{}, fmt.Errorf("invalid manifest sha")
	}
	b, _, err := v.store.get(ctx, ManifestObjectKey(machine, sha))
	if err != nil {
		return model.SnapshotManifest{}, err
	}
	m, gotSHA, err := model.DecodeSnapshotManifest(b)
	if err != nil {
		return model.SnapshotManifest{}, err
	}
	if gotSHA != sha {
		return model.SnapshotManifest{}, fmt.Errorf("manifest %s content hashes to %s: Archive object corrupt or tampered", sha, gotSHA)
	}
	if safeCatalogComponent(m.MachineID) != safeCatalogComponent(machine) {
		return model.SnapshotManifest{}, fmt.Errorf("manifest %s claims machine %q but lives in %q's namespace", sha, m.MachineID, machine)
	}
	return m, nil
}

// OpenBlob returns a verified reader over a blob's uncompressed content.
func (v *V2) OpenBlob(ctx context.Context, key model.BlobKey) (io.ReadCloser, error) {
	rc, err := v.store.getStream(ctx, BlobObjectKey(key))
	if err != nil {
		return nil, err
	}
	return cas.VerifyReader(rc, key)
}

// HasBlob reports whether a blob is present (one HEAD/stat, no bytes).
func (v *V2) HasBlob(ctx context.Context, key model.BlobKey) (bool, error) {
	return v.store.exists(ctx, BlobObjectKey(key))
}

// ForMachine returns the machine-scoped write handle. All v2 writes go
// through a MachineDepot, whose key construction is private and bound to
// this machine ID — foreign-namespace writes are inexpressible (I3).
func (v *V2) ForMachine(machine string) (*MachineDepot, error) {
	if _, err := model.NewMachineID(machine); err != nil {
		return nil, err
	}
	return &MachineDepot{v: v, machine: machine}, nil
}

// MachineDepot is the write surface for exactly one machine's namespace.
type MachineDepot struct {
	v       *V2
	machine string
}

// ParentSnapshot is a latest snapshot actually fetched (and
// identity-verified) from the depot. It is the only source of carried
// blob receipts, so "the parent lists this content" cannot be forged
// with a hand-built manifest.
type ParentSnapshot struct {
	manifest model.SnapshotManifest
	sha      model.ManifestSHA256
	// blobs indexes the manifest's content addresses so CarriedBlob is
	// O(1) per lookup — a per-file linear scan would make a push O(n²)
	// in manifest size, the exact Shlemiel shape v2 exists to remove.
	blobs map[string]bool
}

func (p *ParentSnapshot) Manifest() model.SnapshotManifest { return p.manifest }
func (p *ParentSnapshot) SHA() model.ManifestSHA256        { return p.sha }

// BlobReceipt is proof that a blob is available in the depot: either this
// push uploaded it (EnsureBlob) or the verified parent manifest lists it
// (CarriedBlob). Receipts cannot be constructed outside this package, so
// publishing a manifest that references an unavailable blob is
// unrepresentable (I2).
type blobReceiptKind uint8

const (
	blobReceiptCreated blobReceiptKind = iota + 1
	blobReceiptExisting
	blobReceiptCarried
)

type BlobReceipt struct {
	key  model.BlobKey
	kind blobReceiptKind
}

// PublishedSnapshot is proof that a manifest object exists in the depot.
// Only PublishSnapshot produces a valid value; SetLatest accepts nothing
// else, so the pointer can never reference an unpublished manifest (I2).
type latestExpectationKind uint8

const (
	expectLatestAbsent latestExpectationKind = iota + 1
	expectLatestSHA
)

type latestExpectation struct {
	kind latestExpectationKind
	sha  model.ManifestSHA256
}

func (e latestExpectation) valid() bool {
	return e.kind == expectLatestAbsent || (e.kind == expectLatestSHA && e.sha.Valid())
}

type PublishedSnapshot struct {
	machine  string
	sha      model.ManifestSHA256
	expected latestExpectation
}

func (p PublishedSnapshot) ManifestSHA256() model.ManifestSHA256 { return p.sha }

// StalePublicationError means another publication changed the machine's
// latest pointer after this publication captured its opaque expected parent.
// The stale publication remains immutable history but cannot move latest.
type StalePublicationError struct{ Machine string }

func (e *StalePublicationError) Error() string {
	return fmt.Sprintf("stale publication for machine %s: latest changed since publication began", e.Machine)
}

// Parent fetches the machine's own latest snapshot, if any.
func (m *MachineDepot) Parent(ctx context.Context) (*ParentSnapshot, bool, error) {
	sha, ok, err := m.v.Latest(ctx, m.machine)
	if err != nil || !ok {
		return nil, false, err
	}
	manifest, err := m.v.Manifest(ctx, m.machine, sha)
	if err != nil {
		return nil, false, err
	}
	blobs := make(map[string]bool, len(manifest.Files))
	for _, f := range manifest.Files {
		blobs[f.SHA256] = true
	}
	return &ParentSnapshot{manifest: manifest, sha: sha, blobs: blobs}, true, nil
}

// EnsureBlob stages the file at srcPath as a compressed blob — verifying
// the content hashes to key (I1, via cas) — and writes it to the depot
// unless already present.
func (m *MachineDepot) EnsureBlob(ctx context.Context, key model.BlobKey, srcPath string) (BlobReceipt, error) {
	tmpDir, err := os.MkdirTemp("", "aha-blob-*")
	if err != nil {
		return BlobReceipt{}, err
	}
	defer os.RemoveAll(tmpDir)
	staging, err := cas.Open(tmpDir)
	if err != nil {
		return BlobReceipt{}, err
	}
	if _, err := staging.PutFile(key, srcPath); err != nil {
		return BlobReceipt{}, err
	}
	created, err := m.v.store.putFileIfAbsent(ctx, BlobObjectKey(key), "application/zstd", staging.Path(key))
	if err != nil {
		return BlobReceipt{}, err
	}
	kind := blobReceiptExisting
	if created {
		kind = blobReceiptCreated
	}
	return BlobReceipt{key: key, kind: kind}, nil
}

// CarriedBlob grants a receipt without any depot operation when the
// fetched parent snapshot already lists this content: the parent's own
// publish proved the blob exists, and the depot never deletes (I5).
func (m *MachineDepot) recommitParent(ctx context.Context, parent *ParentSnapshot) error {
	if parent == nil || machinePrefix(parent.manifest.MachineID) != machinePrefix(m.machine) || !parent.sha.Valid() {
		return fmt.Errorf("recommit requires this machine's verified parent")
	}
	return m.SetLatest(ctx, PublishedSnapshot{
		machine:  m.machine,
		sha:      parent.sha,
		expected: latestExpectation{kind: expectLatestSHA, sha: parent.sha},
	})
}

func (m *MachineDepot) CarriedBlob(parent *ParentSnapshot, key model.BlobKey) (BlobReceipt, bool) {
	if parent == nil || !parent.blobs[key.String()] {
		return BlobReceipt{}, false
	}
	return BlobReceipt{key: key, kind: blobReceiptCarried}, true
}

// PublishSnapshot canonically encodes the manifest and writes it to the
// machine's namespace. Every file in the manifest must be covered by a
// receipt, and the manifest must claim this machine's identity.
func (m *MachineDepot) PublishSnapshot(ctx context.Context, manifest model.SnapshotManifest, receipts []BlobReceipt, parent *ParentSnapshot) (PublishedSnapshot, error) {
	if manifest.MachineID != m.machine {
		return PublishedSnapshot{}, fmt.Errorf("manifest claims machine %q; this handle publishes only for %q", manifest.MachineID, m.machine)
	}
	covered := make(map[string]bool, len(receipts))
	for _, r := range receipts {
		if !r.key.Valid() || (r.kind != blobReceiptCreated && r.kind != blobReceiptExisting && r.kind != blobReceiptCarried) {
			return PublishedSnapshot{}, fmt.Errorf("invalid blob receipt")
		}
		covered[r.key.String()] = true
	}
	b, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	for _, f := range manifest.Files {
		if !covered[f.SHA256] {
			return PublishedSnapshot{}, fmt.Errorf("no blob receipt for %s (%s): blobs must be ensured or carried before publish", f.RelativePath, f.SHA256)
		}
	}
	if _, err := m.v.store.putBytesIfAbsent(ctx, ManifestObjectKey(m.machine, sha), "application/json", b); err != nil {
		return PublishedSnapshot{}, err
	}
	expected := latestExpectation{kind: expectLatestAbsent}
	if parent != nil {
		if machinePrefix(parent.manifest.MachineID) != machinePrefix(m.machine) || !parent.sha.Valid() {
			return PublishedSnapshot{}, fmt.Errorf("publication parent does not belong to this machine")
		}
		expected = latestExpectation{kind: expectLatestSHA, sha: parent.sha}
	}
	return PublishedSnapshot{machine: m.machine, sha: sha, expected: expected}, nil
}

// SetLatest moves the machine's pointer to a published snapshot and then
// registers the machine in the index (first push only). The pointer is
// written BEFORE the index entry: the index is the discovery layer, so it
// must only ever name machines whose namespace is complete — a crash
// between the two leaves an undiscoverable but consistent namespace that
// the next push heals, never an indexed machine with no pointer (pinned
// by the fault-injection sweep). An already-current pointer is left
// untouched (steady-state pushes write nothing).
func (m *MachineDepot) SetLatest(ctx context.Context, pub PublishedSnapshot) error {
	if pub.machine != m.machine || !pub.sha.Valid() || !pub.expected.valid() {
		return fmt.Errorf("SetLatest requires a snapshot published with this machine's expected parent")
	}
	key := LatestPointerKey(m.machine)
	pointer, err := EncodeLatestPointer(pub.sha)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < conditionalRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, etag, err := m.v.store.get(ctx, key)
		switch {
		case errors.Is(err, errObjectNotExist):
			if pub.expected.kind != expectLatestAbsent {
				return &StalePublicationError{Machine: m.machine}
			}
			etag = ""
		case err != nil:
			return err
		default:
			current, decodeErr := DecodeLatestPointer(b)
			if decodeErr == nil {
				if current == pub.sha {
					return m.ensureInMachinesIndex(ctx)
				}
				if pub.expected.kind != expectLatestSHA || current != pub.expected.sha {
					return &StalePublicationError{Machine: m.machine}
				}
			}
			// A corrupt pointer is conditionally replaced using the ETag
			// read above. No valid concurrent generation is overwritten.
		}
		err = m.v.store.putBytesConditional(ctx, key, "application/json", pointer, etag)
		if err == nil {
			return m.ensureInMachinesIndex(ctx)
		}
		if !errors.Is(err, errPreconditionFailed) {
			return err
		}
		if attempt+1 == conditionalRetryAttempts {
			break
		}
		if err := waitForConditionalRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return &ContentionError{Key: key, Attempts: conditionalRetryAttempts}
}

func (m *MachineDepot) ensureInMachinesIndex(ctx context.Context) error {
	for attempt := 0; attempt < conditionalRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, etag, err := m.v.store.get(ctx, MachinesIndexKey)
		var machines []string
		switch {
		case errors.Is(err, errObjectNotExist):
			etag = ""
		case err != nil:
			return err
		default:
			machines, err = DecodeMachinesIndex(b)
			if err != nil {
				return err
			}
		}
		for _, existing := range machines {
			if existing == m.machine {
				return nil
			}
		}
		updated, err := EncodeMachinesIndex(append(machines, m.machine))
		if err != nil {
			return err
		}
		err = m.v.store.putBytesConditional(ctx, MachinesIndexKey, "application/json", updated, etag)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errPreconditionFailed) {
			return err
		}
		if attempt+1 == conditionalRetryAttempts {
			break
		}
		if err := waitForConditionalRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return &ContentionError{Key: MachinesIndexKey, Attempts: conditionalRetryAttempts}
}

type ArchiveLatestMetadata struct {
	Machine        string
	ManifestSHA256 model.ManifestSHA256
	Manifest       model.SnapshotManifest
}

type ArchiveMetadataReport struct {
	Initialised bool
	Binding     model.ArchiveBinding
	Machines    int
	Latest      []ArchiveLatestMetadata
	Problems    []string
}

// InspectMetadata validates marker, machine index, latest pointers, and latest
// manifests without probing every blob. It is the bounded status path; Verify
// owns full blob existence/content auditing.
func (v *V2) InspectMetadata(ctx context.Context) (ArchiveMetadataReport, error) {
	report := ArchiveMetadataReport{}
	if err := v.rejectLegacyArchive(ctx); err != nil {
		var legacy *LegacyArchiveError
		if errors.As(err, &legacy) {
			report.Problems = append(report.Problems, "unsupported v1 Archive layout")
			return report, nil
		}
		return report, err
	}
	markerBytes, _, err := v.store.get(ctx, MarkerObjectKey)
	if errors.Is(err, errObjectNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	report.Initialised = true
	report.Binding, err = archiveBindingFromMarker(markerBytes, v.addr)
	if err != nil {
		report.Problems = append(report.Problems, "invalid Archive marker")
		return report, nil
	}
	machines, err := v.Machines(ctx)
	if err != nil {
		return report, err
	}
	report.Machines = len(machines)
	for _, machine := range machines {
		sha, ok, err := v.Latest(ctx, machine)
		if err != nil {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s has an unreadable latest pointer", machine))
			continue
		}
		if !ok {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s is indexed but has no latest pointer", machine))
			continue
		}
		manifest, err := v.Manifest(ctx, machine, sha)
		if err != nil {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s has an unreadable manifest", machine))
			continue
		}
		report.Latest = append(report.Latest, ArchiveLatestMetadata{Machine: machine, ManifestSHA256: sha, Manifest: manifest})
	}
	return report, nil
}

type VerifyOptions struct {
	Progress *ahaprogress.Tracker
}

// Verify audits the depot without progress reporting.
func (v *V2) Verify(ctx context.Context, deep bool) (VerifyReport, error) {
	return v.VerifyWithOptions(ctx, deep, VerifyOptions{})
}

// VerifyWithOptions audits marker, index, pointers, manifests, and blobs.
// Deep mode additionally reads and hashes blob bytes and historical manifests.
func (v *V2) VerifyWithOptions(ctx context.Context, deep bool, opts VerifyOptions) (VerifyReport, error) {
	report := VerifyReport{Deep: deep}
	if err := v.rejectLegacyArchive(ctx); err != nil {
		var legacy *LegacyArchiveError
		if errors.As(err, &legacy) {
			report.Problems = append(report.Problems, "unsupported v1 Archive layout")
			return report, nil
		}
		return report, err
	}
	if b, _, err := v.store.get(ctx, MarkerObjectKey); err == nil {
		if err := validateMarkerV2Bytes(b); err != nil {
			report.Problems = append(report.Problems, "invalid Archive marker")
		}
	} else if errors.Is(err, errObjectNotExist) {
		report.Problems = append(report.Problems, "missing Archive marker")
	} else {
		return report, err
	}
	machines, err := v.Machines(ctx)
	if err != nil {
		return report, err
	}
	report.Machines = len(machines)
	machineTotal := ahaprogress.KnownTotal(uint64(len(machines)))
	processedMachines := uint64(0)
	verifyComplete := false
	blobsComplete := false
	defer func() {
		if !verifyComplete {
			if ctx.Err() != nil {
				opts.Progress.Cancel(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			} else {
				opts.Progress.Fail(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			}
		}
		if deep && !blobsComplete {
			if ctx.Err() != nil {
				opts.Progress.Cancel(ahaprogress.PhaseVerifyBlobs, uint64(report.BytesDownloaded), ahaprogress.UnknownTotal(), ahaprogress.UnitBytes)
			} else {
				opts.Progress.Fail(ahaprogress.PhaseVerifyBlobs, uint64(report.BytesDownloaded), ahaprogress.UnknownTotal(), ahaprogress.UnitBytes)
			}
		}
	}()
	opts.Progress.Start(ahaprogress.PhaseVerify, machineTotal, ahaprogress.UnitMachines)
	if deep {
		opts.Progress.Start(ahaprogress.PhaseVerifyBlobs, ahaprogress.UnknownTotal(), ahaprogress.UnitBytes)
	}
	checkedBlobs := map[string]bool{}
	checkManifest := func(machine string, sha model.ManifestSHA256) error {
		manifest, err := v.Manifest(ctx, machine, sha)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s has an unreadable manifest", machine))
			return nil
		}
		report.Manifests++
		for _, f := range manifest.Files {
			key, err := model.NewBlobKey(f.SHA256)
			if err != nil {
				report.Problems = append(report.Problems, fmt.Sprintf("machine %s manifest contains an invalid blob identity", machine))
				continue
			}
			if checkedBlobs[key.String()] {
				continue
			}
			checkedBlobs[key.String()] = true
			if deep {
				rc, err := v.OpenBlob(ctx, key)
				if err == nil {
					var n int64
					n, err = io.Copy(io.Discard, contextReader{ctx: ctx, r: rc})
					if cerr := rc.Close(); err == nil {
						err = cerr
					}
					report.BytesDownloaded += n
					opts.Progress.Advance(ahaprogress.PhaseVerifyBlobs, uint64(report.BytesDownloaded), ahaprogress.UnknownTotal(), ahaprogress.UnitBytes)
				}
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					report.Problems = append(report.Problems, fmt.Sprintf("machine %s manifest references an unreadable blob", machine))
				}
				continue
			}
			ok, err := v.HasBlob(ctx, key)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
			if !ok {
				report.Problems = append(report.Problems, fmt.Sprintf("machine %s manifest references a missing blob", machine))
			}
		}
		return nil
	}
	seenManifests := map[string]bool{}
	for _, machine := range machines {
		sha, ok, err := v.Latest(ctx, machine)
		if err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s has an unreadable latest pointer", machine))
			processedMachines++
			opts.Progress.Advance(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			continue
		}
		if !ok {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s is indexed but has no latest pointer", machine))
			processedMachines++
			opts.Progress.Advance(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			continue
		}
		seenManifests[ManifestObjectKey(machine, sha)] = true
		if err := checkManifest(machine, sha); err != nil {
			return report, err
		}
		if !deep {
			processedMachines++
			opts.Progress.Advance(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			continue
		}
		lister, ok := v.store.(objectLister)
		if !ok {
			processedMachines++
			opts.Progress.Advance(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
			continue
		}
		keys, err := lister.listKeys(ctx, machinePrefix(machine)+"manifests/")
		if err != nil {
			return report, err
		}
		for _, key := range keys {
			if seenManifests[key] {
				continue
			}
			seenManifests[key] = true
			shaHex := strings.TrimSuffix(key[len(machinePrefix(machine)+"manifests/"):], ".json")
			historical, err := model.NewManifestSHA256(shaHex)
			if err != nil {
				report.Problems = append(report.Problems, fmt.Sprintf("machine %s has an unexpected manifest object", machine))
				continue
			}
			if err := checkManifest(machine, historical); err != nil {
				return report, err
			}
		}
		processedMachines++
		opts.Progress.Advance(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	opts.Progress.Complete(ahaprogress.PhaseVerify, processedMachines, machineTotal, ahaprogress.UnitMachines)
	verifyComplete = true
	if deep {
		opts.Progress.Complete(ahaprogress.PhaseVerifyBlobs, uint64(report.BytesDownloaded), ahaprogress.UnknownTotal(), ahaprogress.UnitBytes)
		blobsComplete = true
	}
	return report, nil
}

func markerV2Bytes() ([]byte, error) {
	now := ahaclock.RealClock{}.Now()
	m := marker{Schema: MarkerSchemaV2, DepotID: fmt.Sprintf("depot-%d", now.UnixNano()), Layout: LayoutVersionV2, CreatedAt: now.Format(time.RFC3339), CreatedBy: "aha " + model.Version}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func validateMarkerV2Bytes(b []byte) error {
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Schema != MarkerSchemaV2 {
		return fmt.Errorf("Archive marker schema %q, want %q", m.Schema, MarkerSchemaV2)
	}
	if m.Layout != LayoutVersionV2 {
		return fmt.Errorf("Archive marker layout %q, want %q", m.Layout, LayoutVersionV2)
	}
	if strings.TrimSpace(m.DepotID) == "" {
		return errors.New("Archive marker identity is missing")
	}
	return nil
}
