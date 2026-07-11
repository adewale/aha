package safety_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

func TestPrepareCorpusDestinationMakesUnownedDirectoryUnrepresentable(t *testing.T) {
	root := t.TempDir()
	unowned := filepath.Join(root, "checkout")
	if err := os.Mkdir(unowned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unowned, "go.mod"), []byte("module unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := safety.PrepareCorpusDestination(model.Config{}, unowned); err == nil {
		t.Fatal("constructed a corpus destination from an unrelated non-empty directory")
	}

	fresh := filepath.Join(root, "fresh")
	destination, err := safety.PrepareCorpusDestination(model.Config{}, fresh)
	if err != nil {
		t.Fatalf("fresh destination rejected: %v", err)
	}
	if path, err := destination.Path(); err != nil || path == "" {
		t.Fatalf("prepared destination path=%q err=%v", path, err)
	}
	var zero safety.CorpusDestination
	if _, err := zero.Path(); err == nil {
		t.Fatal("zero-value corpus destination became a writable path")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("preparing destination mutated filesystem: %v", err)
	}
}

func TestValidateWriteOutsideSourcesRejectsLexicalAndSymlinkContainment(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := model.Config{Sources: []model.SourceConfig{{Type: "pi", Root: source, Enabled: true}}}
	if err := safety.ValidateWriteOutsideSources(cfg, filepath.Join(source, "repo"), "corpus"); err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("lexical containment err=%v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := safety.ValidateWriteOutsideSources(cfg, filepath.Join(link, "repo"), "corpus"); err == nil || !strings.Contains(err.Error(), "must not be inside source root") {
		t.Fatalf("symlink containment err=%v", err)
	}
	if err := safety.ValidateWriteOutsideSources(cfg, filepath.Join(root, "repo"), "corpus"); err != nil {
		t.Fatalf("outside repo rejected: %v", err)
	}
}
