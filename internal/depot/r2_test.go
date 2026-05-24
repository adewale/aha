package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func TestResolveR2ConfigUsesCloudflareEndpointAndAutoRegion(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "acct123")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
	cfg, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://acct123.r2.cloudflarestorage.com" || cfg.Region != "auto" {
		t.Fatalf("bad R2 endpoint/region: %+v", cfg)
	}
}

func TestResolveR2ConfigAllowsEndpointOverrideWithoutAccount(t *testing.T) {
	t.Setenv("AHA_R2_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
	cfg, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint override ignored: %+v", cfg)
	}
}

func TestResolveR2ConfigErrorsDoNotLeakSecrets(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "super-secret")
	_, err := depot.ResolveR2Config(model.R2DepotConfig{})
	if err == nil {
		t.Fatal("expected missing account/endpoint error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestResolveR2ConfigRequiresCredentials(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")
	t.Setenv("AHA_R2_ENDPOINT", "")
	t.Setenv("R2_ENDPOINT", "")
	_, err := depot.ResolveR2Config(model.R2DepotConfig{AccountID: "acct"})
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}
