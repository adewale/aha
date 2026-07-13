package testquality_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestProductVersionSurfacesStayAligned(t *testing.T) {
	packageJSON, err := os.ReadFile(filepath.Join("..", "..", "clients", "typescript", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != model.Version {
		t.Fatalf("TypeScript package version=%q, product version=%q", pkg.Version, model.Version)
	}

	transport, err := os.ReadFile(filepath.Join("..", "..", "clients", "typescript", "transports", "stdio.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transport), `clientInfo: { name: "aha-stdio", version: "`+model.Version+`" }`) {
		t.Fatalf("TypeScript stdio clientInfo does not advertise product version %s", model.Version)
	}
}
