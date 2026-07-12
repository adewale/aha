package depot

import (
	"context"

	"github.com/adewale/aha/internal/model"
)

// Test-only aliases keep low-level typestate invariant tests outside the
// production API. Publication callers cannot access these in normal builds.
type ParentSnapshot = parentSnapshot
type BlobReceipt = blobReceipt
type PublishedSnapshot = publishedSnapshot

type MachineDepot struct{ inner *machineDepot }

func (v *V2) ForMachine(machine string) (*MachineDepot, error) {
	inner, err := v.forMachine(machine)
	if err != nil {
		return nil, err
	}
	return &MachineDepot{inner: inner}, nil
}

func (m *MachineDepot) Parent(ctx context.Context) (*ParentSnapshot, bool, error) {
	return m.inner.parent(ctx)
}
func (m *MachineDepot) EnsureBlob(ctx context.Context, key model.BlobKey, path string) (BlobReceipt, error) {
	return m.inner.ensureBlob(ctx, key, path)
}
func (m *MachineDepot) CarriedBlob(parent *ParentSnapshot, key model.BlobKey) (BlobReceipt, bool) {
	return m.inner.carriedBlob(parent, key)
}
func (m *MachineDepot) PublishSnapshot(ctx context.Context, manifest model.SnapshotManifest, receipts []BlobReceipt, parent *ParentSnapshot) (PublishedSnapshot, error) {
	return m.inner.publishSnapshot(ctx, manifest, receipts, parent)
}
func (m *MachineDepot) SetLatest(ctx context.Context, published PublishedSnapshot) error {
	return m.inner.setLatest(ctx, published)
}
