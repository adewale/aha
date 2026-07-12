package corpus_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

func TestWorkspaceBindingAndMaterialisedVectorPersist(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := model.NewArchiveBinding("archive-1", "local:/archive")
	if err != nil {
		t.Fatal(err)
	}
	if err := corpus.BindWorkspace(store.DB, binding); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "b": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if err := corpus.RecordMaterialisedVector(store.DB, want); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = corpus.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gotBinding, ok, err := corpus.WorkspaceBinding(store.DB)
	if err != nil || !ok || gotBinding != binding {
		t.Fatalf("binding after reopen=(%+v,%v,%v)", gotBinding, ok, err)
	}
	gotVector, err := corpus.MaterialisedVector(store.DB)
	if err != nil || !reflect.DeepEqual(gotVector, want) {
		t.Fatalf("vector after reopen=%v err=%v", gotVector, err)
	}
}

func TestWorkspaceRejectsArchiveMismatchBeforeChangingState(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, _ := model.NewArchiveBinding("archive-1", "local:/one")
	second, _ := model.NewArchiveBinding("archive-2", "local:/two")
	if err := corpus.BindWorkspace(store.DB, first); err != nil {
		t.Fatal(err)
	}
	if err := corpus.BindWorkspace(store.DB, second); !errors.Is(err, corpus.ErrArchiveMismatch) {
		t.Fatalf("BindWorkspace mismatch err=%v", err)
	}
	got, ok, err := corpus.WorkspaceBinding(store.DB)
	if err != nil || !ok || got != first {
		t.Fatalf("binding after rejection=(%+v,%v,%v), want first", got, ok, err)
	}
}

func TestWorkspaceBlobLookupUsesPointQueriesAndCarriesMaterialisedVector(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	store, err := corpus.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.DB.Exec(`insert into snapshots(manifest_sha256,machine_id) values(?,?)`, sha, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`insert into files(file_sha256,kind,bytes,first_seen_manifest_sha256) values(?,?,?,?)`, sha, "session", 1, sha); err != nil {
		t.Fatal(err)
	}
	if err := corpus.RecordMaterialisedVector(store.DB, map[string]string{"a": sha}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	lookup, err := corpus.OpenWorkspaceBlobLookup(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lookup.Close()
	known, _ := model.NewBlobKey(sha)
	missing, _ := model.NewBlobKey("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if ok, err := lookup.Has(known); err != nil || !ok {
		t.Fatalf("known blob=(%v,%v)", ok, err)
	}
	if ok, err := lookup.Has(missing); err != nil || ok {
		t.Fatalf("missing blob=(%v,%v)", ok, err)
	}
	if vector, err := lookup.MaterialisedVector(); err != nil || !reflect.DeepEqual(vector, map[string]string{"a": sha}) {
		t.Fatalf("vector=%v err=%v", vector, err)
	}
}

func TestWorkspaceStateComparesExactLatestVector(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	binding, _ := model.NewArchiveBinding("archive-1", "r2:history")
	if err := corpus.BindWorkspace(store.DB, binding); err != nil {
		t.Fatal(err)
	}
	current := map[string]string{"a": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := corpus.RecordMaterialisedVector(store.DB, current); err != nil {
		t.Fatal(err)
	}
	if got, err := corpus.WorkspaceState(store.DB, binding, current); err != nil || got != model.WorkspaceCurrent {
		t.Fatalf("current state=%s err=%v", got, err)
	}
	advanced := map[string]string{"a": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if got, err := corpus.WorkspaceState(store.DB, binding, advanced); err != nil || got != model.WorkspaceBehind {
		t.Fatalf("advanced state=%s err=%v", got, err)
	}
	if got, err := corpus.MaterialisedVector(store.DB); err != nil || !reflect.DeepEqual(got, current) {
		t.Fatalf("materialised=%v err=%v", got, err)
	}
}
