package depot_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// R2 implements the x-amz-checksum-* family only partially (CRC-64/NVME
// full-object; the rest composite-only), and AWS SDKs have defaulted to
// attaching request checksums since aws-sdk-go-v2 service/s3 v1.73.0.
// Cloudflare's SDK guidance is to compute checksums only when an operation
// requires one, so the production client must pin both directions to
// WhenRequired — the fake-S3 suite accepts any header and can never catch
// a drift here.
func TestNewR2FollowsCloudflareChecksumGuidance(t *testing.T) {
	r2 := depot.NewR2("bucket", depot.R2Config{Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "auto", AccessKeyID: "key", SecretAccessKey: "secret"})
	opts := r2.Client.Options()
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Fatalf("request checksum calculation = %v, want WhenRequired", opts.RequestChecksumCalculation)
	}
	if opts.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenRequired {
		t.Fatalf("response checksum validation = %v, want WhenRequired", opts.ResponseChecksumValidation)
	}
}

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
