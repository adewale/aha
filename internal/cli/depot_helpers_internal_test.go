package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

// captureDepotR2Config must persist the non-secret account id (so a configured
// R2 default survives a new shell) while never writing the access/secret keys
// and never freezing the account-derived endpoint or the default region.
func TestCaptureDepotR2ConfigPersistsAccountNotSecrets(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "acct-123")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secretvalue")
	// Neutralize any endpoint/region the host environment may carry.
	t.Setenv("AHA_R2_ENDPOINT", "")
	t.Setenv("R2_ENDPOINT", "")
	t.Setenv("AHA_R2_REGION", "")
	t.Setenv("R2_REGION", "")

	cfg := model.Config{Depot: model.DepotConfig{Type: "r2", Location: "aha-depot"}}
	if err := captureDepotR2Config(&cfg); err != nil {
		t.Fatalf("captureDepotR2Config: %v", err)
	}
	if cfg.Depot.R2.AccountID != "acct-123" {
		t.Fatalf("account id not persisted: %q", cfg.Depot.R2.AccountID)
	}
	if cfg.Depot.R2.Endpoint != "" {
		t.Fatalf("account-derived endpoint must stay implicit, got %q", cfg.Depot.R2.Endpoint)
	}
	if cfg.Depot.R2.Region != "" {
		t.Fatalf("default region=auto must not be persisted, got %q", cfg.Depot.R2.Region)
	}
	path, err := configWriteForTest(t, cfg)
	if err != nil {
		t.Fatalf("write captured config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "AKIDEXAMPLE") || strings.Contains(text, "secretvalue") {
		t.Fatalf("config leaked R2 credentials: %s", text)
	}
}

func TestCaptureDepotR2ConfigClearsStaleAutoRegion(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "acct-123")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secretvalue")
	t.Setenv("AHA_R2_REGION", "auto")
	t.Setenv("R2_REGION", "")

	cfg := model.Config{Depot: model.DepotConfig{Type: "r2", Location: "aha-depot", R2: model.R2DepotConfig{Region: "us-east-1"}}}
	if err := captureDepotR2Config(&cfg); err != nil {
		t.Fatalf("captureDepotR2Config: %v", err)
	}
	if cfg.Depot.R2.Region != "" {
		t.Fatalf("auto region should clear stale config region, got %q", cfg.Depot.R2.Region)
	}
}

// An explicitly provided endpoint (jurisdiction or fake-S3) is persisted as-is.
func TestCaptureDepotR2ConfigKeepsExplicitEndpoint(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "acct-123")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secretvalue")
	t.Setenv("AHA_R2_ENDPOINT", "https://eu.example.r2.cloudflarestorage.com")
	t.Setenv("R2_ENDPOINT", "")

	cfg := model.Config{Depot: model.DepotConfig{Type: "r2", Location: "aha-depot"}}
	if err := captureDepotR2Config(&cfg); err != nil {
		t.Fatalf("captureDepotR2Config: %v", err)
	}
	if cfg.Depot.R2.Endpoint != "https://eu.example.r2.cloudflarestorage.com" {
		t.Fatalf("explicit endpoint not persisted: %q", cfg.Depot.R2.Endpoint)
	}
}

// Local depots carry no R2 settings, so capture is a no-op.
func TestCaptureDepotR2ConfigLocalNoop(t *testing.T) {
	cfg := model.Config{Depot: model.DepotConfig{Type: "local", Location: "/tmp/x"}}
	if err := captureDepotR2Config(&cfg); err != nil {
		t.Fatalf("local should be a no-op: %v", err)
	}
	if cfg.Depot.R2.AccountID != "" {
		t.Fatal("local depot must not gain r2 config")
	}
}

func TestDepotUninitializedSignal(t *testing.T) {
	cases := []struct {
		name   string
		report depot.VerifyReport
		want   bool
	}{
		{"empty missing marker", depot.VerifyReport{Problems: []string{"missing depot marker"}}, true},
		{"missing marker with catalog refs is degraded", depot.VerifyReport{Problems: []string{"missing depot marker"}, Machines: 1}, false},
		{"missing marker with bundle refs is degraded", depot.VerifyReport{Problems: []string{"missing depot marker"}, Manifests: 1}, false},
		{"no problems", depot.VerifyReport{}, false},
		{"invalid marker", depot.VerifyReport{Problems: []string{"invalid depot marker: bad schema"}}, false},
		{"marker plus other", depot.VerifyReport{Problems: []string{"missing depot marker", "catalog missing bundle x"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := depotUninitialized(tc.report); got != tc.want {
				t.Fatalf("depotUninitialized(%+v) = %v, want %v", tc.report, got, tc.want)
			}
		})
	}
}

func configWriteForTest(t *testing.T, cfg model.Config) (string, error) {
	t.Helper()
	return config.Write(filepath.Join(t.TempDir(), "config.jsonc"), cfg, "// test config\n")
}
