package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestReadmeDocumentsRegisteredCommandsAndPrivacy(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, name := range cli.CommandNames() {
		if !strings.Contains(text, "aha "+name) {
			t.Fatalf("README does not document command %s", name)
		}
	}
	if !strings.Contains(text, "does **not** redact secrets") {
		t.Fatalf("README privacy warning missing")
	}
}
