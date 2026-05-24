package safety_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/safety"
)

func TestRootCapabilities(t *testing.T) {
	for name, fn := range map[string]func(string) (string, error){
		"source": func(s string) (string, error) { v, err := safety.NewSourceRoot(s); return v.String(), err },
		"corpus": func(s string) (string, error) { v, err := safety.NewCorpusRoot(s); return v.String(), err },
		"depot":  func(s string) (string, error) { v, err := safety.NewDepotRoot(s); return v.String(), err },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fn(""); err == nil {
				t.Fatal("empty root accepted")
			}
			got, err := fn("/tmp/aha/../aha-root")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, "/aha-root") {
				t.Fatalf("root not cleaned: %q", got)
			}
		})
	}
}

func TestSafeRelPathRejectsUnsafe(t *testing.T) {
	if _, err := safety.NewSafeRelPath("ok/file.txt"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "/abs", "a/../../b", ""} {
		if _, err := safety.NewSafeRelPath(bad); err == nil {
			t.Fatalf("unsafe rel path accepted: %q", bad)
		}
	}
}
