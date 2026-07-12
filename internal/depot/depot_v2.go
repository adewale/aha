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
	addr        Address
	store       objectStore
	markerStore objectStore
}

const currentArchiveDataPrefix = "aha-v3/"

func (v *V2) markerObjects() objectStore {
	if v.markerStore != nil {
		return v.markerStore
	}
	return v.store
}

func (v *V2) withLegacyData() *V2 {
	base := v.markerObjects()
	return &V2{addr: v.addr, markerStore: base, store: base}
}

func (v *V2) withCurrentData() *V2 {
	base := v.markerObjects()
	return &V2{addr: v.addr, markerStore: base, store: &prefixedStore{base: base, prefix: currentArchiveDataPrefix}}
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

// UnsupportedArchiveFeatureError is returned before an Archive capability is
// constructed. This prevents an older reader or writer from partially
// interpreting a format that requires newer semantics.
type UnsupportedArchiveFeatureError struct {
	Feature string
}

func (e *UnsupportedArchiveFeatureError) Error() string {
	return fmt.Sprintf("Archive requires unsupported feature %q; upgrade aha before using this Archive", e.Feature)
}

type ArchiveWriteUpgradeRequiredError struct{}

func (*ArchiveWriteUpgradeRequiredError) Error() string {
	return "Archive is readable but its writer generation is unsupported; initialise a fresh Archive before uploading"
}

type UnsupportedArchiveFormatError struct {
	FoundMajor     int
	SupportedMajor int
	FoundMinor     int
	SupportedMinor int
}

type UnsupportedSnapshotAdapterError struct {
	Adapter          string
	RequiredVersion  string
	SupportedVersion string
}

func (e *UnsupportedSnapshotAdapterError) Error() string {
	if e.SupportedVersion != "" {
		return fmt.Sprintf("snapshot requires adapter %q version %q (this aha supports %q); upgrade aha before materialising this Archive", e.Adapter, e.RequiredVersion, e.SupportedVersion)
	}
	return fmt.Sprintf("snapshot requires unsupported adapter %q; upgrade aha before materialising this Archive", e.Adapter)
}

func (e *UnsupportedArchiveFormatError) Error() string {
	if e.FoundMajor == e.SupportedMajor {
		return fmt.Sprintf("Archive format minor %d is not supported (this aha supports up to %d); upgrade aha before using this Archive", e.FoundMinor, e.SupportedMinor)
	}
	return fmt.Sprintf("Archive format major %d is not supported (this aha supports %d); upgrade aha before using this Archive", e.FoundMajor, e.SupportedMajor)
}

type CorruptArchiveObjectError struct {
	Object string
	Cause  error
}

func (e *CorruptArchiveObjectError) Error() string {
	return "Archive object " + e.Object + " is invalid"
}
func (e *CorruptArchiveObjectError) Unwrap() error { return e.Cause }

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
	base := &localStoreV2{root: expanded}
	return &V2{addr: Address{Type: "local", Location: expanded}, store: base, markerStore: base}, nil
}

// NewV2FromR2 opens a depot v2 over an existing R2 driver's bucket+client.
func NewV2FromR2(r *R2) *V2 {
	base := &r2StoreV2{bucket: r.Bucket().String(), client: r.S3Client()}
	return &V2{addr: r.Address(), store: base, markerStore: base}
}

func (v *V2) Address() Address { return v.addr }

// Init provisions the depot marker. It refuses to initialize over a v1
// layout: the two layouts never silently mix, and there is no migration
// (pre-release decision; v1 bundles remain importable via `aha ingest`).
func (v *V2) Init(ctx context.Context) error {
	if v.markerStore == nil {
		v.markerStore = v.store
	}
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return err
	}
	b, _, err := v.markerObjects().get(ctx, MarkerObjectKey)
	if err == nil {
		if err := validateMarkerForWrite(b); err != nil {
			return err
		}
		v.store = v.withCurrentData().store
		return nil
	}
	if !errors.Is(err, errObjectNotExist) {
		return err
	}
	mb, err := markerV2Bytes()
	if err != nil {
		return err
	}
	if err := v.markerObjects().putBytesConditional(ctx, MarkerObjectKey, "application/json", mb, ""); err != nil {
		if errors.Is(err, errPreconditionFailed) {
			b, _, getErr := v.markerObjects().get(ctx, MarkerObjectKey)
			if getErr != nil {
				return getErr
			}
			if err := validateMarkerForWrite(b); err != nil {
				return err
			}
			v.store = v.withCurrentData().store
			return nil
		}
		return err
	}
	v.store = v.withCurrentData().store
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
	depot    *V2
	binding  model.ArchiveBinding
	state    model.ArchiveState
	machines map[string]bool
}

// DownloadPlan freezes the Archive latest vector before any Workspace is
// opened. Effectful download code accepts this opaque plan, not raw addresses.
type DownloadPlan struct {
	reader PreparedPull
	latest map[string]model.ManifestSHA256
}

// PreparePull performs the complete metadata preflight required before a
// caller creates or opens a writable corpus.
func (v *V2) PreparePull(ctx context.Context) (PreparedPull, error) {
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return PreparedPull{}, err
	}
	marker, _, err := v.markerObjects().get(ctx, MarkerObjectKey)
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
	parsed, _ := decodeAndValidateMarkerForRead(marker)
	data := v.withLegacyData()
	if parsed.Schema == MarkerSchemaCurrent {
		data = v.withCurrentData()
	}
	machines, err := data.Machines(ctx)
	if err != nil {
		return PreparedPull{}, err
	}
	return PreparedPull{depot: data, machines: append([]string(nil), machines...), binding: binding}, nil
}

func (v *V2) PrepareUpload(ctx context.Context) (PreparedUpload, error) {
	if err := v.rejectLegacyArchive(ctx); err != nil {
		return PreparedUpload{}, err
	}
	markerBytes, _, err := v.markerObjects().get(ctx, MarkerObjectKey)
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
	if err := validateMarkerForWrite(markerBytes); err != nil {
		return PreparedUpload{}, err
	}
	data := v.withCurrentData()
	machines, err := data.Machines(ctx)
	if err != nil {
		return PreparedUpload{}, err
	}
	indexed := make(map[string]bool, len(machines))
	for _, machine := range machines {
		indexed[machine] = true
	}
	state := model.ArchiveEmpty
	if len(machines) > 0 {
		state = model.ArchivePopulated
	}
	transition := model.ArchiveTransition(state, model.ArchiveUpload)
	if !transition.Allowed {
		return PreparedUpload{}, fmt.Errorf("Archive state %s does not allow upload", state)
	}
	return PreparedUpload{depot: data, binding: binding, state: state, machines: indexed}, nil
}

// DamagedArchiveError prevents writer-capability construction from a metadata
// graph that status classifies as damaged.
type DamagedArchiveError struct{ Problems []string }

func (e *DamagedArchiveError) Error() string {
	return "Archive is damaged; run `aha archive verify --deep`"
}

func (v *V2) rejectLegacyArchive(ctx context.Context) error {
	if _, _, err := v.markerObjects().get(ctx, "depot.json"); err == nil {
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
	if p.depot == nil || !p.binding.Valid() || p.machines == nil || (p.state != model.ArchiveEmpty && p.state != model.ArchivePopulated) {
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
		sha, ok, err := p.latestSnapshot(ctx, machine)
		if err != nil {
			return DownloadPlan{}, err
		}
		if !ok {
			return DownloadPlan{}, fmt.Errorf("Archive machine %q has no latest snapshot", machine)
		}
		latest[machine] = sha
	}
	return DownloadPlan{reader: p, latest: latest}, nil
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

// SelectedManifest returns only the manifest named by this plan's frozen
// latest vector; callers cannot supply a historical identity.
func (p DownloadPlan) SelectedManifest(ctx context.Context, machine string) (model.SnapshotManifest, error) {
	return p.selectedManifest(ctx, machine)
}

func (p DownloadPlan) selectedPreparedManifest(ctx context.Context, machine string) (model.PreparedSnapshot, error) {
	if p.reader.depot == nil || p.latest == nil {
		return model.PreparedSnapshot{}, errors.New("invalid Archive download plan")
	}
	sha, selected := p.latest[machine]
	if !selected {
		return model.PreparedSnapshot{}, errors.New("machine is outside the frozen latest vector")
	}
	return p.reader.depot.preparedManifest(ctx, machine, sha)
}

func (p DownloadPlan) selectedManifest(ctx context.Context, machine string) (model.SnapshotManifest, error) {
	prepared, err := p.selectedPreparedManifest(ctx, machine)
	if err != nil {
		return model.SnapshotManifest{}, err
	}
	return prepared.Manifest(), nil
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

// RequireAdapters checks only changed-machine manifests and does so before a
// writable Workspace is opened. Unsupported snapshots remain durable in the
// Archive but cannot make this Workspace falsely current.
func (p DownloadPlan) RequireAdapters(ctx context.Context, materialised map[string]string, supported map[string]string) error {
	machines, err := p.Machines()
	if err != nil {
		return err
	}
	for _, machine := range machines {
		sha := p.latest[machine]
		if materialised[machine] == sha.String() {
			continue
		}
		manifest, err := p.selectedManifest(ctx, machine)
		if err != nil {
			return err
		}
		for _, requirement := range manifest.Adapters {
			version, ok := supported[requirement.Name]
			if !ok {
				return &UnsupportedSnapshotAdapterError{Adapter: requirement.Name, RequiredVersion: requirement.Version}
			}
			if version != requirement.Version {
				return &UnsupportedSnapshotAdapterError{Adapter: requirement.Name, RequiredVersion: requirement.Version, SupportedVersion: version}
			}
		}
	}
	return nil
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
		manifest, err := p.selectedManifest(ctx, machine)
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

func (p PreparedPull) latestSnapshot(ctx context.Context, machine string) (model.ManifestSHA256, bool, error) {
	if p.depot == nil {
		return model.ManifestSHA256{}, false, errors.New("invalid prepared pull capability")
	}
	return p.depot.Latest(ctx, machine)
}

func (p PreparedPull) manifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.SnapshotManifest, error) {
	if p.depot == nil {
		return model.SnapshotManifest{}, errors.New("invalid prepared pull capability")
	}
	return p.depot.Manifest(ctx, machine, sha)
}

func (p PreparedPull) openBlob(ctx context.Context, key model.BlobKey) (io.ReadCloser, error) {
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
		return model.ManifestSHA256{}, false, &CorruptArchiveObjectError{Object: LatestPointerKey(machine), Cause: err}
	}
	return sha, true, nil
}

// Manifest fetches and verifies one snapshot manifest: the bytes must be
// canonical, hash to the requested identity, and belong to the requested
// machine's namespace.
func (v *V2) Manifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.SnapshotManifest, error) {
	prepared, err := v.preparedManifest(ctx, machine, sha)
	if err != nil {
		return model.SnapshotManifest{}, err
	}
	return prepared.Manifest(), nil
}

func (v *V2) preparedManifest(ctx context.Context, machine string, sha model.ManifestSHA256) (model.PreparedSnapshot, error) {
	if !sha.Valid() {
		return model.PreparedSnapshot{}, fmt.Errorf("invalid manifest sha")
	}
	b, _, err := v.store.get(ctx, ManifestObjectKey(machine, sha))
	if err != nil {
		return model.PreparedSnapshot{}, err
	}
	prepared, err := model.DecodePreparedSnapshot(b)
	if err != nil {
		return model.PreparedSnapshot{}, &CorruptArchiveObjectError{Object: ManifestObjectKey(machine, sha), Cause: err}
	}
	if prepared.SHA() != sha {
		return model.PreparedSnapshot{}, &CorruptArchiveObjectError{Object: ManifestObjectKey(machine, sha), Cause: fmt.Errorf("content hashes to %s, want %s", prepared.SHA(), sha)}
	}
	if safeCatalogComponent(prepared.Manifest().MachineID) != safeCatalogComponent(machine) {
		return model.PreparedSnapshot{}, &CorruptArchiveObjectError{Object: ManifestObjectKey(machine, sha), Cause: fmt.Errorf("claims machine %q, want %q", prepared.Manifest().MachineID, machine)}
	}
	return prepared, nil
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
// through a machineDepot, whose key construction is private and bound to
// this machine ID — foreign-namespace writes are inexpressible (I3).
func (v *V2) forMachine(machine string) (*machineDepot, error) {
	if _, err := model.NewMachineID(machine); err != nil {
		return nil, err
	}
	return &machineDepot{v: v, machine: machine}, nil
}

// machineDepot is the write surface for exactly one machine's namespace.
type machineDepot struct {
	v       *V2
	machine string
}

// parentSnapshot is a latest snapshot actually fetched (and
// identity-verified) from the depot. It is the only source of carried
// blob receipts, so "the parent lists this content" cannot be forged
// with a hand-built manifest.
type parentSnapshot struct {
	manifest model.SnapshotManifest
	sha      model.ManifestSHA256
	// blobs indexes the manifest's content addresses so CarriedBlob is
	// O(1) per lookup — a per-file linear scan would make a push O(n²)
	// in manifest size, the exact Shlemiel shape v2 exists to remove.
	blobs map[string]bool
}

func (p *parentSnapshot) Manifest() model.SnapshotManifest { return p.manifest }
func (p *parentSnapshot) SHA() model.ManifestSHA256        { return p.sha }

// blobReceipt is proof that a blob is available in the depot: either this
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

type blobReceipt struct {
	key  model.BlobKey
	kind blobReceiptKind
}

// publishedSnapshot is proof that a manifest object exists in the depot.
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

type publishedSnapshot struct {
	machine  string
	sha      model.ManifestSHA256
	expected latestExpectation
}

func (p publishedSnapshot) ManifestSHA256() model.ManifestSHA256 { return p.sha }

// StalePublicationError means another publication changed the machine's
// latest pointer after this publication captured its opaque expected parent.
// The stale publication remains immutable history but cannot move latest.
type StalePublicationError struct{ Machine string }

func (e *StalePublicationError) Error() string {
	return fmt.Sprintf("stale publication for machine %s: latest changed since publication began", e.Machine)
}

// Parent fetches the machine's own latest snapshot, if any.
func (m *machineDepot) parent(ctx context.Context) (*parentSnapshot, bool, error) {
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
	return &parentSnapshot{manifest: manifest, sha: sha, blobs: blobs}, true, nil
}

// EnsureBlob stages the file at srcPath as a compressed blob — verifying
// the content hashes to key (I1, via cas) — and writes it to the depot
// unless already present.
func (m *machineDepot) ensureBlob(ctx context.Context, key model.BlobKey, srcPath string) (blobReceipt, error) {
	tmpDir, err := os.MkdirTemp("", "aha-blob-*")
	if err != nil {
		return blobReceipt{}, err
	}
	defer os.RemoveAll(tmpDir)
	staging, err := cas.Open(tmpDir)
	if err != nil {
		return blobReceipt{}, err
	}
	if _, err := staging.PutFile(key, srcPath); err != nil {
		return blobReceipt{}, err
	}
	created, err := m.v.store.putFileIfAbsent(ctx, BlobObjectKey(key), "application/zstd", staging.Path(key))
	if err != nil {
		return blobReceipt{}, err
	}
	kind := blobReceiptExisting
	if created {
		kind = blobReceiptCreated
	}
	return blobReceipt{key: key, kind: kind}, nil
}

// CarriedBlob grants a receipt without any depot operation when the
// fetched parent snapshot already lists this content: the parent's own
// publish proved the blob exists, and the depot never deletes (I5).
func (m *machineDepot) recommitParent(ctx context.Context, parent *parentSnapshot) error {
	if parent == nil || machinePrefix(parent.manifest.MachineID) != machinePrefix(m.machine) || !parent.sha.Valid() {
		return fmt.Errorf("recommit requires this machine's verified parent")
	}
	return m.setLatest(ctx, publishedSnapshot{
		machine:  m.machine,
		sha:      parent.sha,
		expected: latestExpectation{kind: expectLatestSHA, sha: parent.sha},
	})
}

func (m *machineDepot) carriedBlob(parent *parentSnapshot, key model.BlobKey) (blobReceipt, bool) {
	if parent == nil || !parent.blobs[key.String()] {
		return blobReceipt{}, false
	}
	return blobReceipt{key: key, kind: blobReceiptCarried}, true
}

// PublishSnapshot canonically encodes the manifest and writes it to the
// machine's namespace. Every file in the manifest must be covered by a
// receipt, and the manifest must claim this machine's identity.
func (m *machineDepot) publishSnapshot(ctx context.Context, manifest model.SnapshotManifest, receipts []blobReceipt, parent *parentSnapshot) (publishedSnapshot, error) {
	if manifest.MachineID != m.machine {
		return publishedSnapshot{}, fmt.Errorf("manifest claims machine %q; this handle publishes only for %q", manifest.MachineID, m.machine)
	}
	covered := make(map[string]bool, len(receipts))
	for _, r := range receipts {
		if !r.key.Valid() || (r.kind != blobReceiptCreated && r.kind != blobReceiptExisting && r.kind != blobReceiptCarried) {
			return publishedSnapshot{}, fmt.Errorf("invalid blob receipt")
		}
		covered[r.key.String()] = true
	}
	b, sha, err := model.EncodeSnapshotManifest(manifest)
	if err != nil {
		return publishedSnapshot{}, err
	}
	for _, f := range manifest.Files {
		if !covered[f.SHA256] {
			return publishedSnapshot{}, fmt.Errorf("no blob receipt for %s (%s): blobs must be ensured or carried before publish", f.RelativePath, f.SHA256)
		}
	}
	if _, err := m.v.store.putBytesIfAbsent(ctx, ManifestObjectKey(m.machine, sha), "application/json", b); err != nil {
		return publishedSnapshot{}, err
	}
	expected := latestExpectation{kind: expectLatestAbsent}
	if parent != nil {
		if machinePrefix(parent.manifest.MachineID) != machinePrefix(m.machine) || !parent.sha.Valid() {
			return publishedSnapshot{}, fmt.Errorf("publication parent does not belong to this machine")
		}
		expected = latestExpectation{kind: expectLatestSHA, sha: parent.sha}
	}
	return publishedSnapshot{machine: m.machine, sha: sha, expected: expected}, nil
}

// SetLatest moves the machine's pointer to a published snapshot and then
// registers the machine in the index (first push only). The pointer is
// written BEFORE the index entry: the index is the discovery layer, so it
// must only ever name machines whose namespace is complete — a crash
// between the two leaves an undiscoverable but consistent namespace that
// the next push heals, never an indexed machine with no pointer (pinned
// by the fault-injection sweep). An already-current pointer is left
// untouched (steady-state pushes write nothing).
func (m *machineDepot) setLatest(ctx context.Context, pub publishedSnapshot) error {
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

func (m *machineDepot) ensureInMachinesIndex(ctx context.Context) error {
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
	StateSHA256    string
	CapturedAt     string
	Files          int
}

type ArchiveMetadataReport struct {
	Initialised     bool
	UpgradeRequired bool
	Binding         model.ArchiveBinding
	Machines        int
	Latest          []ArchiveLatestMetadata
	Problems        []string
	plan            *DownloadPlan
}

// DownloadPlan returns the frozen latest vector produced by the same metadata
// inspection, avoiding duplicate index/pointer requests in unified status.
func (r ArchiveMetadataReport) DownloadPlan() (DownloadPlan, bool) {
	if r.plan == nil {
		return DownloadPlan{}, false
	}
	return DownloadPlan{reader: r.plan.reader, latest: cloneLatestVector(r.plan.latest)}, true
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
	markerBytes, _, err := v.markerObjects().get(ctx, MarkerObjectKey)
	if errors.Is(err, errObjectNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	report.Initialised = true
	report.Binding, err = archiveBindingFromMarker(markerBytes, v.addr)
	if err != nil {
		var feature *UnsupportedArchiveFeatureError
		var format *UnsupportedArchiveFormatError
		if errors.As(err, &feature) || errors.As(err, &format) {
			report.UpgradeRequired = true
			report.Problems = append(report.Problems, "Archive requires a newer aha")
			return report, nil
		}
		report.Problems = append(report.Problems, "invalid Archive marker")
		return report, nil
	}
	parsed, _ := decodeAndValidateMarkerForRead(markerBytes)
	data := v.withLegacyData()
	if parsed.Schema == MarkerSchemaCurrent {
		data = v.withCurrentData()
	}
	machines, err := data.Machines(ctx)
	if err != nil {
		return report, err
	}
	report.Machines = len(machines)
	report.Latest, report.Problems, err = data.inspectLatestMetadata(ctx, machines)
	if err == nil && len(report.Problems) == 0 && len(report.Latest) == len(machines) {
		latest := make(map[string]model.ManifestSHA256, len(report.Latest))
		for _, item := range report.Latest {
			latest[item.Machine] = item.ManifestSHA256
		}
		plan := DownloadPlan{reader: PreparedPull{depot: data, machines: append([]string(nil), machines...), binding: report.Binding}, latest: latest}
		report.plan = &plan
	}
	return report, err
}

func (v *V2) validateLatestMetadataForWrite(ctx context.Context, machines []string) ([]string, error) {
	var problems []string
	for _, machine := range machines {
		sha, ok, err := v.Latest(ctx, machine)
		if err != nil {
			var corrupt *CorruptArchiveObjectError
			if errors.As(err, &corrupt) {
				problems = append(problems, fmt.Sprintf("machine %s has an unreadable latest pointer", machine))
				continue
			}
			return nil, err
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("machine %s is indexed but has no latest pointer", machine))
			continue
		}
		if _, err := v.Manifest(ctx, machine, sha); err != nil {
			var corrupt *CorruptArchiveObjectError
			if errors.Is(err, errObjectNotExist) || errors.As(err, &corrupt) {
				problems = append(problems, fmt.Sprintf("machine %s has an unreadable manifest", machine))
				continue
			}
			return nil, err
		}
	}
	return problems, nil
}

func (v *V2) inspectLatestMetadata(ctx context.Context, machines []string) ([]ArchiveLatestMetadata, []string, error) {
	latest := make([]ArchiveLatestMetadata, 0, len(machines))
	var problems []string
	for _, machine := range machines {
		sha, ok, err := v.Latest(ctx, machine)
		if err != nil {
			var corrupt *CorruptArchiveObjectError
			if errors.As(err, &corrupt) {
				problems = append(problems, fmt.Sprintf("machine %s has an unreadable latest pointer", machine))
				continue
			}
			return latest, problems, err
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("machine %s is indexed but has no latest pointer", machine))
			continue
		}
		manifest, err := v.Manifest(ctx, machine, sha)
		if err != nil {
			var corrupt *CorruptArchiveObjectError
			if errors.Is(err, errObjectNotExist) || errors.As(err, &corrupt) {
				problems = append(problems, fmt.Sprintf("machine %s has an unreadable manifest", machine))
				continue
			}
			return latest, problems, err
		}
		state, err := model.SnapshotStateSHA256(manifest)
		if err != nil {
			problems = append(problems, fmt.Sprintf("machine %s has an unreadable manifest state", machine))
			continue
		}
		latest = append(latest, ArchiveLatestMetadata{Machine: machine, ManifestSHA256: sha, StateSHA256: state.String(), CapturedAt: manifest.CapturedAt, Files: len(manifest.Files)})
	}
	return latest, problems, nil
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
	markerBytes, _, markerErr := v.markerObjects().get(ctx, MarkerObjectKey)
	data := v.withLegacyData()
	if markerErr == nil {
		parsed, err := decodeAndValidateMarkerForRead(markerBytes)
		if err != nil {
			var feature *UnsupportedArchiveFeatureError
			var format *UnsupportedArchiveFormatError
			if errors.As(err, &feature) || errors.As(err, &format) {
				return report, err
			}
			report.Problems = append(report.Problems, "invalid Archive marker")
		} else if parsed.Schema == MarkerSchemaCurrent {
			data = v.withCurrentData()
		}
	} else if errors.Is(markerErr, errObjectNotExist) {
		report.Problems = append(report.Problems, "missing Archive marker")
	} else {
		return report, markerErr
	}
	machines, err := data.Machines(ctx)
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
		manifest, err := data.Manifest(ctx, machine, sha)
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
				rc, err := data.OpenBlob(ctx, key)
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
			ok, err := data.HasBlob(ctx, key)
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
		sha, ok, err := data.Latest(ctx, machine)
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
		lister, ok := data.store.(objectLister)
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

const (
	archiveFormatMajor = 3
	archiveFormatMinor = 0
	// ArchiveWriterFenceFeature is required in every Archive this build may
	// open. The immediately preceding binary does not recognise it and
	// therefore refuses the Archive before publication.
	ArchiveWriterFenceFeature = "writer-fence-v1"
)

var supportedArchiveFeatures = map[string]struct{}{
	"full-manifest-v2":        {},
	ArchiveWriterFenceFeature: {},
}

func markerV2Bytes() ([]byte, error) {
	now := ahaclock.RealClock{}.Now()
	m := marker{
		Schema:           MarkerSchemaCurrent,
		DepotID:          fmt.Sprintf("depot-%d", now.UnixNano()),
		Layout:           LayoutVersionCurrent,
		FormatMajor:      archiveFormatMajor,
		FormatMinor:      archiveFormatMinor,
		RequiredFeatures: []string{"full-manifest-v2", ArchiveWriterFenceFeature},
		CreatedAt:        now.Format(time.RFC3339),
		CreatedBy:        "aha " + model.Version,
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decodeAndValidateMarkerForRead(b []byte) (marker, error) {
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return marker{}, err
	}
	if strings.TrimSpace(m.DepotID) == "" {
		return marker{}, errors.New("Archive marker identity is missing")
	}
	legacy := m.Schema == MarkerSchemaV2 && m.Layout == LayoutVersionV2
	current := m.Schema == MarkerSchemaCurrent && m.Layout == LayoutVersionCurrent
	if !legacy && !current {
		return marker{}, &UnsupportedArchiveFormatError{FoundMajor: m.FormatMajor, SupportedMajor: archiveFormatMajor, FoundMinor: m.FormatMinor, SupportedMinor: archiveFormatMinor}
	}
	major, supportedMajor, supportedMinor := m.FormatMajor, archiveFormatMajor, archiveFormatMinor
	if legacy {
		supportedMajor, supportedMinor = 2, 0
		if major == 0 {
			major = 2
		}
	}
	if major != supportedMajor || m.FormatMinor > supportedMinor {
		return marker{}, &UnsupportedArchiveFormatError{FoundMajor: major, SupportedMajor: supportedMajor, FoundMinor: m.FormatMinor, SupportedMinor: supportedMinor}
	}
	seen := make(map[string]string, len(m.RequiredFeatures)+len(m.OptionalFeatures))
	for _, feature := range m.RequiredFeatures {
		if err := validateArchiveFeatureName(feature); err != nil {
			return marker{}, err
		}
		if previous, ok := seen[feature]; ok {
			return marker{}, fmt.Errorf("Archive feature %q is declared more than once (%s and required)", feature, previous)
		}
		seen[feature] = "required"
		if _, ok := supportedArchiveFeatures[feature]; !ok {
			return marker{}, &UnsupportedArchiveFeatureError{Feature: feature}
		}
	}
	// The original v2 marker predates feature declarations; its exact
	// schema/layout pair implicitly means full-manifest-v2 for read-only
	// compatibility. Current markers must declare it explicitly.
	if _, ok := seen["full-manifest-v2"]; !ok && current {
		return marker{}, fmt.Errorf("Archive marker is missing required baseline feature %q", "full-manifest-v2")
	}
	if current {
		if _, ok := seen[ArchiveWriterFenceFeature]; !ok {
			return marker{}, fmt.Errorf("Archive marker is missing required writer fence feature %q", ArchiveWriterFenceFeature)
		}
	}
	for _, feature := range m.OptionalFeatures {
		if err := validateArchiveFeatureName(feature); err != nil {
			return marker{}, err
		}
		if previous, ok := seen[feature]; ok {
			return marker{}, fmt.Errorf("Archive feature %q is declared more than once (%s and optional)", feature, previous)
		}
		seen[feature] = "optional"
	}
	return m, nil
}

func validateMarkerV2Bytes(b []byte) error {
	_, err := decodeAndValidateMarkerForRead(b)
	return err
}

func validateMarkerForWrite(b []byte) error {
	m, err := decodeAndValidateMarkerForRead(b)
	if err != nil {
		return err
	}
	if m.Schema != MarkerSchemaCurrent || m.Layout != LayoutVersionCurrent {
		return &ArchiveWriteUpgradeRequiredError{}
	}
	return nil
}

func validateArchiveFeatureName(feature string) error {
	if strings.TrimSpace(feature) == "" || feature != strings.TrimSpace(feature) {
		return errors.New("Archive marker contains an invalid empty feature name")
	}
	for _, r := range feature {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("Archive feature %q contains an invalid character", feature)
		}
	}
	return nil
}
