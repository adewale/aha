package safety_test

import (
	"path/filepath"
	"testing"
	"testing/quick"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/safety"
)

func TestContainsMatchesFilepathRelProperty(t *testing.T) {
	root := t.TempDir()
	prop := func(child string) bool {
		target := filepath.Join(root, safePathComponent(child))
		if !safety.Contains(root, target) {
			return false
		}
		outside := filepath.Join(filepath.Dir(root), "outside-"+safePathComponent(child))
		if outside != root && safety.Contains(root, outside) {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWriteOutsideSourcesRejectsGeneratedChildrenProperty(t *testing.T) {
	root := t.TempDir()
	cfg := model.Config{Sources: []model.SourceConfig{{Type: "pi", Root: root, Enabled: true}}}
	prop := func(child string) bool {
		target := filepath.Join(root, safePathComponent(child))
		err := safety.ValidateWriteOutsideSources(cfg, target, "target")
		if err == nil {
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func safePathComponent(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "child"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}
