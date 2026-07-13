package depot_test

import (
	"errors"
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
func TestParseExplicitAddressRejectsAmbiguousBareLocation(t *testing.T) {
	if _, err := depot.ParseExplicitAddress("looks-like-a-bucket"); err == nil || !strings.Contains(err.Error(), "r2:") || !strings.Contains(err.Error(), "local:") {
		t.Fatalf("error=%v want explicit-prefix guidance", err)
	}
	for _, value := range []string{"r2:bucket", "local:/tmp/depot"} {
		if _, err := depot.ParseExplicitAddress(value); err != nil {
			t.Fatalf("ParseExplicitAddress(%q): %v", value, err)
		}
	}
}

func TestR2ConfigurationErrorsIdentifySafeFieldWithoutValue(t *testing.T) {
	secretCanary := "your-secret-canary"
	_, err := depot.NewR2Credentials("real-access-id", secretCanary)
	var configErr *depot.R2ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error=%v want *R2ConfigError", err)
	}
	if configErr.Field() != depot.R2ConfigSecretAccessKey || configErr.Kind() != depot.R2ConfigPlaceholder {
		t.Fatalf("field=%q kind=%q", configErr.Field(), configErr.Kind())
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Fatalf("typed error leaked secret: %v", err)
	}
	if !strings.Contains(err.Error(), "secret access key") || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("typed error is not actionable: %v", err)
	}
}

func TestNewR2FollowsCloudflareChecksumGuidance(t *testing.T) {
	t.Setenv("AHA_R2_ENDPOINT", "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
	cfg, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := depot.ParseR2Bucket("bucket")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := depot.NewR2(bucket, cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts := r2.S3Client().Options()
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Fatalf("request checksum calculation = %v, want WhenRequired", opts.RequestChecksumCalculation)
	}
	if opts.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenRequired {
		t.Fatalf("response checksum validation = %v, want WhenRequired", opts.ResponseChecksumValidation)
	}
}

func TestResolveR2ConfigExplicitIgnoresAmbientProductionCredentials(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "production-access-canary")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "production-secret-canary")
	t.Setenv("AHA_R2_ACCOUNT_ID", "fedcba9876543210fedcba9876543210")
	credentials, err := depot.NewR2Credentials("smoketest-access", "smoketest-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := depot.ResolveR2ConfigExplicit(model.R2ArchiveConfig{AccountID: "0123456789abcdef0123456789abcdef"}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID() != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("explicit config used ambient production account: %s", cfg.AccountID())
	}
}

func TestNewR2CredentialsRejectsMissingAndPlaceholderValuesWithoutLeaking(t *testing.T) {
	for _, tc := range []struct{ access, secret string }{{"", "secret-canary"}, {"access-canary", ""}, {"<access>", "secret-canary"}, {"access-canary", "<secret>"}} {
		_, err := depot.NewR2Credentials(tc.access, tc.secret)
		if err == nil {
			t.Fatalf("credentials (%q,%q) accepted", tc.access, tc.secret)
		}
		for _, canary := range []string{"access-canary", "secret-canary"} {
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("credential error leaked supplied value: %v", err)
			}
		}
	}
}

func TestResolveR2ConfigUsesCloudflareEndpointAndAutoRegion(t *testing.T) {
	const accountID = "0123456789abcdef0123456789abcdef"
	t.Setenv("AHA_R2_ACCOUNT_ID", accountID)
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
	cfg, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint() != "https://"+accountID+".r2.cloudflarestorage.com" || cfg.Region() != "auto" {
		t.Fatalf("bad R2 endpoint/region: endpoint=%s region=%s", cfg.Endpoint(), cfg.Region())
	}
}

func TestResolveR2ConfigAllowsEndpointOverrideWithoutAccount(t *testing.T) {
	t.Setenv("AHA_R2_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
	cfg, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint() != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint override ignored: %s", cfg.Endpoint())
	}
}

func TestResolveR2ConfigErrorsDoNotLeakSecrets(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "super-secret")
	_, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err == nil {
		t.Fatal("expected missing account/endpoint error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestResolveR2ConfigRejectsConflictingAliasesWithoutLeakingValues(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "0123456789abcdef0123456789abcdef")
	t.Setenv("R2_ACCOUNT_ID", "fedcba9876543210fedcba9876543210")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "access-canary-a")
	t.Setenv("R2_ACCESS_KEY_ID", "access-canary-b")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret-canary-a")
	t.Setenv("R2_SECRET_ACCESS_KEY", "secret-canary-b")
	_, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err == nil {
		t.Fatal("conflicting aliases were silently resolved by precedence")
	}
	message := err.Error()
	for _, name := range []string{"AHA_R2_ACCOUNT_ID", "R2_ACCOUNT_ID", "AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID", "AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY"} {
		if !strings.Contains(message, name) {
			t.Fatalf("error=%q missing conflicting variable %s", message, name)
		}
	}
	for _, secret := range []string{"access-canary-a", "access-canary-b", "secret-canary-a", "secret-canary-b"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked conflicting value %q: %s", secret, message)
		}
	}
}

func TestResolveR2ConfigRequiresCredentials(t *testing.T) {
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")
	t.Setenv("AHA_R2_ENDPOINT", "")
	t.Setenv("R2_ENDPOINT", "")
	_, err := depot.ResolveR2Config(model.R2ArchiveConfig{AccountID: "0123456789abcdef0123456789abcdef"})
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}

func TestNewR2RejectsZeroValueValidatedInputs(t *testing.T) {
	if _, err := depot.NewR2(depot.R2Bucket{}, depot.R2Config{}); err == nil {
		t.Fatal("NewR2 accepted zero-value validated inputs")
	}
}

func TestParseR2BucketRejectsInvalidAndPlaceholderValues(t *testing.T) {
	for _, value := range []string{"", "<your-production-bucket>", "<bucket>", "...", "UPPER", "https://example.com/bucket", "a/b", "-leading", "trailing-"} {
		t.Run(value, func(t *testing.T) {
			if _, err := depot.ParseR2Bucket(value); err == nil {
				t.Fatalf("ParseR2Bucket(%q) succeeded", value)
			}
		})
	}
	bucket, err := depot.ParseR2Bucket("aha-depot-123")
	if err != nil || bucket.String() != "aha-depot-123" {
		t.Fatalf("valid bucket rejected: bucket=%q err=%v", bucket.String(), err)
	}
}

func TestParseAddressRejectsInvalidR2BucketBeforeNetworking(t *testing.T) {
	if _, err := depot.ParseAddress("r2:<your-production-bucket>"); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("ParseAddress placeholder error=%v", err)
	}
}

func TestResolveR2ConfigRejectsPlaceholderAndMalformedAccountIDs(t *testing.T) {
	for _, accountID := range []string{"<your-account-id>", "...", "acct123", strings.Repeat("g", 32)} {
		t.Run(accountID, func(t *testing.T) {
			t.Setenv("AHA_R2_ACCOUNT_ID", accountID)
			t.Setenv("AHA_R2_ENDPOINT", "")
			t.Setenv("R2_ENDPOINT", "")
			t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
			t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
			if _, err := depot.ResolveR2Config(model.R2ArchiveConfig{}); err == nil {
				t.Fatalf("ResolveR2Config accepted account ID %q", accountID)
			}
		})
	}
}

func TestResolveR2ConfigRejectsUnsafeEndpointsBeforeNetworking(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/path", "<endpoint>"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("AHA_R2_ENDPOINT", endpoint)
			t.Setenv("AHA_R2_ACCOUNT_ID", "")
			t.Setenv("R2_ACCOUNT_ID", "")
			t.Setenv("AHA_R2_ACCESS_KEY_ID", "key")
			t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "secret")
			if _, err := depot.ResolveR2Config(model.R2ArchiveConfig{}); err == nil {
				t.Fatalf("ResolveR2Config accepted unsafe endpoint %q", endpoint)
			}
		})
	}
}

func TestResolveR2ConfigRejectsPlaceholderCredentialsWithoutLeakingThem(t *testing.T) {
	t.Setenv("AHA_R2_ACCOUNT_ID", "0123456789abcdef0123456789abcdef")
	t.Setenv("AHA_R2_ACCESS_KEY_ID", "<r2-access-key-id>")
	t.Setenv("AHA_R2_SECRET_ACCESS_KEY", "<r2-secret-access-key>")
	_, err := depot.ResolveR2Config(model.R2ArchiveConfig{})
	if err == nil {
		t.Fatal("ResolveR2Config accepted placeholder credentials")
	}
	if strings.Contains(err.Error(), "r2-secret-access-key") {
		t.Fatalf("error leaked secret placeholder: %v", err)
	}
}
