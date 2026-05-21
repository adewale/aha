package safety_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

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
