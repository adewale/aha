package safety_test

import (
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := safety.PrepareWorkspaceDestination(model.Config{}, unowned); err == nil {
		t.Fatal("constructed a corpus destination from an unrelated non-empty directory")
	}

	fresh := filepath.Join(root, "fresh")
	destination, err := safety.PrepareWorkspaceDestination(model.Config{}, fresh)
	if err != nil {
		t.Fatalf("fresh destination rejected: %v", err)
	}
	if path, err := destination.Path(); err != nil || path == "" {
		t.Fatalf("prepared destination path=%q err=%v", path, err)
	}
	var zero safety.WorkspaceDestination
	if _, err := zero.Path(); err == nil {
		t.Fatal("zero-value corpus destination became a writable path")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("preparing destination mutated filesystem: %v", err)
	}
}

func TestClaimRejectsUnsafeUnixParentBeforeDestinationMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix parent mode contract")
	}
	root := t.TempDir()
	unsafeParent := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafeParent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeParent, 0o777); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(unsafeParent, "workspace")
	destination, err := safety.PrepareWorkspaceDestination(model.Config{}, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Claim(); err == nil {
		t.Fatal("claimed destination under unsafe non-sticky parent")
	}
	for _, path := range []string{target, filepath.Join(unsafeParent, ".workspace.aha-workspace")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected claim mutated %s: %v", path, err)
		}
	}
}

func TestPreparedWorkspaceDestinationRejectsSymlinkSubstitutionAtClaim(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := model.Config{Sources: []model.SourceConfig{{Type: "pi", Root: source, Enabled: true}}}
	target := filepath.Join(root, "workspace")
	destination, err := safety.PrepareWorkspaceDestination(cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := destination.Claim(); err == nil {
		t.Fatal("claimed destination after symlink substitution")
	}
	if _, err := os.Stat(filepath.Join(source, model.WorkspaceDatabaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("substitution created a database in source: %v", err)
	}
}

func TestClaimedWorkspaceRootIgnoresPublicPathSubstitution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows claim denies rename/delete while the directory handle is held")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "workspace")
	destination, err := safety.PrepareWorkspaceDestination(model.Config{Sources: []model.SourceConfig{{Type: "pi", Root: source, Enabled: true}}}, target)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := destination.Claim()
	if err != nil {
		t.Fatal(err)
	}
	defer claimed.Close()
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claimed.StoragePath(), "proof"), []byte("anchored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "proof")); !os.IsNotExist(err) {
		t.Fatalf("public substitution redirected claimed write: %v", err)
	}
	if runtime.GOOS != "windows" && claimed.IdentityPath() == target {
		t.Fatal("claimed lifecycle identity still uses replaceable public path")
	}
}

func TestPreparedWorkspaceDestinationRejectsSourceRenameSubstitutionAtClaim(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := model.Config{Sources: []model.SourceConfig{{Type: "pi", Root: source, Enabled: true}}}
	target := filepath.Join(root, "workspace")
	destination, err := safety.PrepareWorkspaceDestination(cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Claim(); err == nil {
		t.Fatal("claimed destination after source directory substitution")
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
