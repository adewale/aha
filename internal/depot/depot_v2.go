package depot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/adewale/aha/internal/cas"
	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/model"
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
	return &V2{addr: r.Address(), store: &r2StoreV2{bucket: r.Bucket, client: r.Client}}
}

func (v *V2) Address() Address { return v.addr }

// Init provisions the depot marker. It refuses to initialize over a v1
// layout: the two layouts never silently mix, and there is no migration
// (pre-release decision; v1 bundles remain importable via `aha ingest`).
func (v *V2) Init(ctx context.Context) error {
	if r2, ok := v.store.(*r2StoreV2); ok {
		if err := r2.ensureBucket(ctx); err != nil {
			return err
		}
	}
	if _, _, err := v.store.get(ctx, "depot.json"); err == nil {
		return fmt.Errorf("depot at %s uses the v1 bundle layout; depot v2 does not migrate it (export/import v1 bundles instead)", v.addr.Location)
	} else if !errors.Is(err, errObjectNotExist) {
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
		return model.SnapshotManifest{}, fmt.Errorf("manifest %s content hashes to %s: depot object corrupt or tampered", sha, gotSHA)
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
type BlobReceipt struct {
	key model.BlobKey
}

// PublishedSnapshot is proof that a manifest object exists in the depot.
// Only PublishSnapshot produces a valid value; SetLatest accepts nothing
// else, so the pointer can never reference an unpublished manifest (I2).
type PublishedSnapshot struct {
	machine string
	sha     model.ManifestSHA256
}

func (p PublishedSnapshot) ManifestSHA256() model.ManifestSHA256 { return p.sha }

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
	if _, err := m.v.store.putFileIfAbsent(ctx, BlobObjectKey(key), "application/zstd", staging.Path(key)); err != nil {
		return BlobReceipt{}, err
	}
	return BlobReceipt{key: key}, nil
}

// CarriedBlob grants a receipt without any depot operation when the
// fetched parent snapshot already lists this content: the parent's own
// publish proved the blob exists, and the depot never deletes (I5).
func (m *MachineDepot) CarriedBlob(parent *ParentSnapshot, key model.BlobKey) (BlobReceipt, bool) {
	if parent == nil || !parent.blobs[key.String()] {
		return BlobReceipt{}, false
	}
	return BlobReceipt{key: key}, true
}

// PublishSnapshot canonically encodes the manifest and writes it to the
// machine's namespace. Every file in the manifest must be covered by a
// receipt, and the manifest must claim this machine's identity.
func (m *MachineDepot) PublishSnapshot(ctx context.Context, manifest model.SnapshotManifest, receipts []BlobReceipt) (PublishedSnapshot, error) {
	if manifest.MachineID != m.machine {
		return PublishedSnapshot{}, fmt.Errorf("manifest claims machine %q; this handle publishes only for %q", manifest.MachineID, m.machine)
	}
	covered := make(map[string]bool, len(receipts))
	for _, r := range receipts {
		if !r.key.Valid() {
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
	return PublishedSnapshot{machine: m.machine, sha: sha}, nil
}

const conditionalPutAttempts = 5

// SetLatest moves the machine's pointer to a published snapshot and
// registers the machine in the index on first push. An already-current
// pointer is left untouched (steady-state pushes write nothing).
func (m *MachineDepot) SetLatest(ctx context.Context, pub PublishedSnapshot) error {
	if pub.machine != m.machine || !pub.sha.Valid() {
		return fmt.Errorf("SetLatest requires a snapshot published by this machine's handle")
	}
	if err := m.ensureInMachinesIndex(ctx); err != nil {
		return err
	}
	key := LatestPointerKey(m.machine)
	pointer, err := EncodeLatestPointer(pub.sha)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < conditionalPutAttempts; attempt++ {
		b, etag, err := m.v.store.get(ctx, key)
		switch {
		case errors.Is(err, errObjectNotExist):
			etag = ""
		case err != nil:
			return err
		default:
			// A corrupt pointer deliberately falls through: the etag
			// came from reading that very object, so the conditional
			// PUT below replaces it with a valid pointer (self-heal).
			current, decodeErr := DecodeLatestPointer(b)
			if decodeErr == nil && current == pub.sha {
				return nil
			}
		}
		err = m.v.store.putBytesConditional(ctx, key, "application/json", pointer, etag)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errPreconditionFailed) {
			return err
		}
	}
	return fmt.Errorf("latest pointer update conflict for %s", key)
}

func (m *MachineDepot) ensureInMachinesIndex(ctx context.Context) error {
	for attempt := 0; attempt < conditionalPutAttempts; attempt++ {
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
	}
	return fmt.Errorf("machines index update conflict")
}

// Verify audits the depot: marker, index, every machine's pointer and
// latest manifest identity, and referenced blob presence; deep mode also
// verifies blob content and audits historical manifests. Verify is the
// only v2 path allowed to LIST (I6) and uses it only in deep mode.
func (v *V2) Verify(ctx context.Context, deep bool) (VerifyReport, error) {
	report := VerifyReport{Deep: deep}
	if b, _, err := v.store.get(ctx, MarkerObjectKey); err == nil {
		if err := validateMarkerV2Bytes(b); err != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+err.Error())
		}
	} else if errors.Is(err, errObjectNotExist) {
		report.Problems = append(report.Problems, "missing depot marker")
	} else {
		return report, err
	}
	machines, err := v.Machines(ctx)
	if err != nil {
		return report, err
	}
	report.Catalogs = len(machines)
	checkedBlobs := map[string]bool{}
	checkManifest := func(machine string, sha model.ManifestSHA256) error {
		manifest, err := v.Manifest(ctx, machine, sha)
		if err != nil {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s manifest %s: %v", machine, sha, err))
			return nil
		}
		report.Bundles++
		for _, f := range manifest.Files {
			key, err := model.NewBlobKey(f.SHA256)
			if err != nil {
				report.Problems = append(report.Problems, fmt.Sprintf("manifest %s file %s: %v", sha, f.RelativePath, err))
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
					n, err = io.Copy(io.Discard, rc)
					if cerr := rc.Close(); err == nil {
						err = cerr
					}
					report.BytesDownloaded += n
				}
				if err != nil {
					report.Problems = append(report.Problems, fmt.Sprintf("blob %s: %v", key, err))
				}
				continue
			}
			ok, err := v.HasBlob(ctx, key)
			if err != nil {
				return err
			}
			if !ok {
				report.Problems = append(report.Problems, fmt.Sprintf("manifest %s references missing blob %s", sha, key))
			}
		}
		return nil
	}
	seenManifests := map[string]bool{}
	for _, machine := range machines {
		sha, ok, err := v.Latest(ctx, machine)
		if err != nil {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s pointer: %v", machine, err))
			continue
		}
		if !ok {
			report.Problems = append(report.Problems, fmt.Sprintf("machine %s is indexed but has no latest pointer", machine))
			continue
		}
		seenManifests[ManifestObjectKey(machine, sha)] = true
		if err := checkManifest(machine, sha); err != nil {
			return report, err
		}
		if !deep {
			continue
		}
		lister, ok := v.store.(objectLister)
		if !ok {
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
				report.Problems = append(report.Problems, fmt.Sprintf("unexpected manifest object key %s", key))
				continue
			}
			if err := checkManifest(machine, historical); err != nil {
				return report, err
			}
		}
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
		return fmt.Errorf("depot marker schema %q, want %q", m.Schema, MarkerSchemaV2)
	}
	if m.Layout != LayoutVersionV2 {
		return fmt.Errorf("depot marker layout %q, want %q", m.Layout, LayoutVersionV2)
	}
	return nil
}
