package fixtureaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticRoot materialises a fixture root that satisfies every declaration,
// so a test can add exactly one deviation and attribute the failure to it.
func syntheticRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name := range handFixtureSources {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name := range corpusSources {
		dir := filepath.Join(root, "corpora", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInventoryAttributesSyntheticRoot(t *testing.T) {
	fixtures, err := Inventory(syntheticRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	want := len(handFixtureSources) + len(corpusSources)
	if len(fixtures) != want {
		t.Fatalf("inventory returned %d fixtures, want %d", len(fixtures), want)
	}
	for i := 1; i < len(fixtures); i++ {
		if fixtures[i-1].Label >= fixtures[i].Label {
			t.Fatalf("inventory is not ordered by label: %q then %q", fixtures[i-1].Label, fixtures[i].Label)
		}
	}
}

// TestInventoryRejectsUnattributedFixture is the gate that makes a new fixture
// a deliberate act: an unclassified source cannot join the corpus silently and
// then go uncounted by the coverage and version gates.
func TestInventoryRejectsUnattributedFixture(t *testing.T) {
	root := syntheticRoot(t)
	if err := os.WriteFile(filepath.Join(root, "mystery_agent.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Inventory(root)
	if err == nil {
		t.Fatal("inventory accepted a fixture with no declared source")
	}
	if !strings.Contains(err.Error(), "mystery_agent.jsonl") {
		t.Fatalf("error must name the offending fixture: %v", err)
	}
}

func TestInventoryRejectsUnattributedCorpus(t *testing.T) {
	root := syntheticRoot(t)
	dir := filepath.Join(root, "corpora", "mystery-sample")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Inventory(root)
	if err == nil {
		t.Fatal("inventory accepted a corpus directory with no declared source")
	}
	if !strings.Contains(err.Error(), "mystery-sample") {
		t.Fatalf("error must name the offending corpus: %v", err)
	}
}

// TestInventoryRejectsMissingDeclaredFixture keeps the declaration honest in
// the other direction: deleting a fixture to make a gate pass fails instead.
func TestInventoryRejectsMissingDeclaredFixture(t *testing.T) {
	root := syntheticRoot(t)
	if err := os.Remove(filepath.Join(root, "opencode_realish.jsonl")); err != nil {
		t.Fatal(err)
	}
	_, err := Inventory(root)
	if err == nil {
		t.Fatal("inventory accepted a root missing a declared fixture")
	}
	if !strings.Contains(err.Error(), "opencode_realish.jsonl") {
		t.Fatalf("error must name the missing fixture: %v", err)
	}
}

func TestInventoryRejectsMissingDeclaredCorpus(t *testing.T) {
	root := syntheticRoot(t)
	if err := os.RemoveAll(filepath.Join(root, "corpora", "pi-mono-sample")); err != nil {
		t.Fatal(err)
	}
	_, err := Inventory(root)
	if err == nil {
		t.Fatal("inventory accepted a root missing a declared corpus")
	}
	if !strings.Contains(err.Error(), "pi-mono-sample") {
		t.Fatalf("error must name the missing corpus: %v", err)
	}
}

// TestSourcesCoversEveryDeclaration pins the source list the audit iterates:
// every attributed source is reported, so an adapter cannot be omitted from
// the audit by being omitted from a list.
func TestSourcesCoversEveryDeclaration(t *testing.T) {
	declared := map[string]struct{}{}
	for _, s := range handFixtureSources {
		declared[s] = struct{}{}
	}
	for _, s := range corpusSources {
		declared[s] = struct{}{}
	}
	got := Sources()
	if len(got) != len(declared) {
		t.Fatalf("Sources() = %v, want %d distinct sources", got, len(declared))
	}
	for _, s := range got {
		if _, ok := declared[s]; !ok {
			t.Fatalf("Sources() reported undeclared source %q", s)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("Sources() is not sorted: %v", got)
		}
	}
}
