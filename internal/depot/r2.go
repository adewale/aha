package depot

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// R2Bucket is a Cloudflare R2 bucket name that has passed syntax and
// placeholder validation. Its representation is opaque so networking code
// cannot be constructed with an unchecked bucket string.
type R2Bucket struct{ value string }

func ParseR2Bucket(value string) (R2Bucket, error) {
	value = strings.TrimSpace(value)
	if looksLikePlaceholder(value) {
		return R2Bucket{}, fmt.Errorf("R2 bucket contains a documentation placeholder; replace it with the real bucket name")
	}
	if len(value) < 3 || len(value) > 63 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return R2Bucket{}, fmt.Errorf("invalid R2 bucket %q: use 3-63 lowercase letters, numbers, and hyphens", value)
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return R2Bucket{}, fmt.Errorf("invalid R2 bucket %q: use 3-63 lowercase letters, numbers, and hyphens", value)
		}
	}
	return R2Bucket{value: value}, nil
}

func (b R2Bucket) String() string { return b.value }
func (b R2Bucket) Valid() bool    { return b.value != "" }

// R2AccountID is a validated Cloudflare account identifier. Cloudflare
// account IDs are 32 lowercase hexadecimal characters.
type R2AccountID struct{ value string }

func ParseR2AccountID(value string) (R2AccountID, error) {
	value = strings.TrimSpace(value)
	if err := r2ConfigValueError(R2ConfigAccountID, value); err != nil {
		return R2AccountID{}, err
	}
	if len(value) != 32 {
		return R2AccountID{}, &R2ConfigError{field: R2ConfigAccountID, kind: R2ConfigInvalid}
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			return R2AccountID{}, &R2ConfigError{field: R2ConfigAccountID, kind: R2ConfigInvalid}
		}
	}
	return R2AccountID{value: value}, nil
}

func (id R2AccountID) String() string { return id.value }

func looksLikePlaceholder(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return trimmed == "" || trimmed == "..." || strings.Contains(trimmed, "<") || strings.Contains(trimmed, ">") || strings.HasPrefix(trimmed, "your-")
}

// R2ConfigField identifies a configuration field without carrying its value.
// Public error boundaries may safely expose this metadata.
type R2ConfigField string

const (
	R2ConfigAccountID       R2ConfigField = "account_id"
	R2ConfigEndpoint        R2ConfigField = "endpoint"
	R2ConfigRegion          R2ConfigField = "region"
	R2ConfigAccessKeyID     R2ConfigField = "access_key_id"
	R2ConfigSecretAccessKey R2ConfigField = "secret_access_key"
)

// R2ConfigErrorKind is the finite set of locally correctable configuration
// failures. It intentionally excludes raw values.
type R2ConfigErrorKind string

const (
	R2ConfigMissing     R2ConfigErrorKind = "missing"
	R2ConfigPlaceholder R2ConfigErrorKind = "placeholder"
	R2ConfigInvalid     R2ConfigErrorKind = "invalid"
)

// R2ConfigError reports only safe field/reason metadata. The rejected value is
// never retained, so downstream presentation cannot accidentally disclose a
// credential.
type R2ConfigError struct {
	field R2ConfigField
	kind  R2ConfigErrorKind
}

func (e *R2ConfigError) Field() R2ConfigField    { return e.field }
func (e *R2ConfigError) Kind() R2ConfigErrorKind { return e.kind }
func (e *R2ConfigError) Error() string {
	label := map[R2ConfigField]string{
		R2ConfigAccountID: "account ID", R2ConfigEndpoint: "endpoint", R2ConfigRegion: "region",
		R2ConfigAccessKeyID: "access key ID", R2ConfigSecretAccessKey: "secret access key",
	}[e.field]
	if label == "" {
		label = "configuration"
	}
	switch e.kind {
	case R2ConfigMissing:
		return "R2 " + label + " is required"
	case R2ConfigPlaceholder:
		return "R2 " + label + " contains a documentation placeholder"
	default:
		return "R2 " + label + " is invalid"
	}
}

func r2ConfigValueError(field R2ConfigField, value string) error {
	if strings.TrimSpace(value) == "" {
		return &R2ConfigError{field: field, kind: R2ConfigMissing}
	}
	if looksLikePlaceholder(value) {
		return &R2ConfigError{field: field, kind: R2ConfigPlaceholder}
	}
	return nil
}

// R2Config is a resolved, validated R2 client configuration. Secret-bearing
// fields are private and the type has no JSON representation, so config writers
// cannot accidentally persist credentials.
type R2Config struct {
	accountID       R2AccountID
	endpoint        string
	region          string
	accessKeyID     string
	secretAccessKey string
}

func (c R2Config) AccountID() string { return c.accountID.String() }
func (c R2Config) Endpoint() string  { return c.endpoint }
func (c R2Config) Region() string    { return c.region }
func (c R2Config) Valid() bool {
	return c.endpoint != "" && c.region == "auto" && c.accessKeyID != "" && c.secretAccessKey != ""
}

// R2Credentials is an opaque matching S3 key pair. Explicit consumers such as
// live smoke tests can carry a test-only capability without consulting ambient
// production environment variables.
type R2Credentials struct {
	accessKeyID     string
	secretAccessKey string
}

func NewR2Credentials(accessKeyID, secretAccessKey string) (R2Credentials, error) {
	accessKeyID = strings.TrimSpace(accessKeyID)
	secretAccessKey = strings.TrimSpace(secretAccessKey)
	if err := r2ConfigValueError(R2ConfigAccessKeyID, accessKeyID); err != nil {
		return R2Credentials{}, err
	}
	if err := r2ConfigValueError(R2ConfigSecretAccessKey, secretAccessKey); err != nil {
		return R2Credentials{}, err
	}
	return R2Credentials{accessKeyID: accessKeyID, secretAccessKey: secretAccessKey}, nil
}

func ResolveR2Config(cfg model.R2DepotConfig) (R2Config, error) {
	pairs := [][2]string{
		{"AHA_R2_ACCOUNT_ID", "R2_ACCOUNT_ID"},
		{"AHA_R2_ENDPOINT", "R2_ENDPOINT"},
		{"AHA_R2_REGION", "R2_REGION"},
		{"AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID"},
		{"AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY"},
	}
	var conflicts []string
	for _, pair := range pairs {
		primary, alias := os.Getenv(pair[0]), os.Getenv(pair[1])
		if primary != "" && alias != "" && primary != alias {
			conflicts = append(conflicts, pair[0]+" and "+pair[1])
		}
	}
	if len(conflicts) > 0 {
		return R2Config{}, fmt.Errorf("conflicting R2 environment aliases are set to different values: %s; unset one variable from each pair", strings.Join(conflicts, ", "))
	}
	explicit := model.R2DepotConfig{
		AccountID: firstEnv("AHA_R2_ACCOUNT_ID", "R2_ACCOUNT_ID", cfg.AccountID),
		Endpoint:  firstEnv("AHA_R2_ENDPOINT", "R2_ENDPOINT", cfg.Endpoint),
		Region:    firstEnv("AHA_R2_REGION", "R2_REGION", cfg.Region),
	}
	creds, err := NewR2Credentials(
		firstEnv("AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID", ""),
		firstEnv("AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY", ""),
	)
	if err != nil {
		return R2Config{}, err
	}
	return ResolveR2ConfigExplicit(explicit, creds)
}

// ResolveR2ConfigExplicit resolves only its arguments. It never reads the
// process environment, which makes production-credential fallback impossible
// for smoke tests and other isolated callers.
func ResolveR2ConfigExplicit(cfg model.R2DepotConfig, creds R2Credentials) (R2Config, error) {
	accountValue := strings.TrimSpace(cfg.AccountID)
	endpoint := strings.TrimSpace(cfg.Endpoint)
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}
	if region != "auto" {
		kind := R2ConfigInvalid
		if looksLikePlaceholder(region) {
			kind = R2ConfigPlaceholder
		}
		return R2Config{}, &R2ConfigError{field: R2ConfigRegion, kind: kind}
	}
	if creds.accessKeyID == "" || creds.secretAccessKey == "" {
		return R2Config{}, fmt.Errorf("explicit R2 configuration requires validated credentials")
	}
	var accountID R2AccountID
	if endpoint == "" {
		if accountValue == "" {
			return R2Config{}, &R2ConfigError{field: R2ConfigAccountID, kind: R2ConfigMissing}
		}
		var err error
		accountID, err = ParseR2AccountID(accountValue)
		if err != nil {
			return R2Config{}, err
		}
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID.String())
	} else {
		if looksLikePlaceholder(endpoint) {
			return R2Config{}, &R2ConfigError{field: R2ConfigEndpoint, kind: R2ConfigPlaceholder}
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return R2Config{}, &R2ConfigError{field: R2ConfigEndpoint, kind: R2ConfigInvalid}
		}
		if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return R2Config{}, &R2ConfigError{field: R2ConfigEndpoint, kind: R2ConfigInvalid}
		}
		host := strings.ToLower(parsed.Hostname())
		local := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && local) {
			return R2Config{}, &R2ConfigError{field: R2ConfigEndpoint, kind: R2ConfigInvalid}
		}
		if accountValue != "" {
			accountID, err = ParseR2AccountID(accountValue)
			if err != nil {
				return R2Config{}, err
			}
		}
	}
	return R2Config{accountID: accountID, endpoint: endpoint, region: region, accessKeyID: creds.accessKeyID, secretAccessKey: creds.secretAccessKey}, nil
}

type R2 struct {
	bucket R2Bucket
	client *s3.Client
	config R2Config
}

func NewR2(bucket R2Bucket, cfg R2Config) (*R2, error) {
	if !bucket.Valid() || !cfg.Valid() {
		return nil, fmt.Errorf("NewR2 requires a validated bucket and resolved configuration")
	}
	// R2 implements x-amz-checksum-* only partially, so follow Cloudflare's
	// SDK guidance and send/validate checksums only when an operation
	// requires one. Integrity is owned by the depot layer anyway: every blob
	// and manifest is content-addressed and re-hashed on read.
	client := s3.New(s3.Options{Region: cfg.region, BaseEndpoint: aws.String(cfg.endpoint), Credentials: credentials.NewStaticCredentialsProvider(cfg.accessKeyID, cfg.secretAccessKey, ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &R2{bucket: bucket, client: client, config: cfg}, nil
}

// NewR2WithClient binds a validated bucket to an existing S3 client. It is
// primarily the protocol-test seam; production configuration uses NewR2.
func NewR2WithClient(bucket R2Bucket, client *s3.Client) (*R2, error) {
	if !bucket.Valid() || client == nil {
		return nil, fmt.Errorf("NewR2WithClient requires a validated bucket and non-nil client")
	}
	return &R2{bucket: bucket, client: client}, nil
}

func (d *R2) Address() Address     { return Address{Type: "r2", Location: d.bucket.String()} }
func (d *R2) Bucket() R2Bucket     { return d.bucket }
func (d *R2) S3Client() *s3.Client { return d.client }

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		return true
	default:
		return false
	}
}

func firstEnv(a, b, fallback string) string {
	if v := os.Getenv(a); v != "" {
		return v
	}
	if v := os.Getenv(b); v != "" {
		return v
	}
	return fallback
}
