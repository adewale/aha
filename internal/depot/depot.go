package depot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
)

const (
	DefaultR2Bucket = "aha-archive"
)

type Address struct {
	Type     string
	Location string
}

func (a Address) String() string { return fmt.Sprintf("%s:%s", a.Type, a.Location) }

type VerifyReport struct {
	Manifests       int      `json:"manifests"`
	Machines        int      `json:"machines"`
	Repaired        bool     `json:"repaired"`
	Deep            bool     `json:"deep"`
	BytesRead       int64    `json:"bytes_read"`
	BytesDownloaded int64    `json:"bytes_downloaded"`
	Problems        []string `json:"problems,omitempty"`
}

// ExplicitAddressError means a CLI destination omitted its Archive kind. It
// carries no rejected value, so it is safe for public presentation.
type ExplicitAddressError struct{}

func (*ExplicitAddressError) Error() string {
	return "explicit Archive address required: prefix the destination with r2: or local:"
}

// ParseExplicitAddress parses an address supplied at a CLI boundary. Unlike
// ParseAddress's backward-compatible config/default form, it cannot silently
// reinterpret a bucket-looking value as a local filesystem path.
func ParseExplicitAddress(s string) (Address, error) {
	trimmed := strings.TrimSpace(s)
	if _, _, ok := strings.Cut(trimmed, ":"); !ok {
		return Address{}, &ExplicitAddressError{}
	}
	return ParseAddress(trimmed)
}

func ParseAddress(s string) (Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Address{Type: "local", Location: "~/.aha/archive"}, nil
	}
	if s == "r2" {
		return Address{Type: "r2", Location: DefaultR2Bucket}, nil
	}
	typ, loc, ok := strings.Cut(s, ":")
	if !ok {
		return Address{Type: "local", Location: s}, nil
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	loc = strings.TrimSpace(loc)
	if typ == "r2" && loc == "" {
		loc = DefaultR2Bucket
	}
	if typ == "local" && loc == "" {
		return Address{}, fmt.Errorf("local Archive path required")
	}
	if typ != "local" && typ != "r2" {
		return Address{}, fmt.Errorf("unsupported Archive type %q", typ)
	}
	if typ == "r2" {
		bucket, err := ParseR2Bucket(loc)
		if err != nil {
			return Address{}, err
		}
		loc = bucket.String()
	}
	return Address{Type: typ, Location: loc}, nil
}

func AddressFromConfig(cfg model.ArchiveConfig) (Address, error) {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	if typ == "" {
		return Address{Type: "local", Location: "~/.aha/archive"}, nil
	}
	switch typ {
	case "local":
		if strings.TrimSpace(cfg.Location) == "" {
			return Address{}, fmt.Errorf("local Archive path required")
		}
		return Address{Type: typ, Location: strings.TrimSpace(cfg.Location)}, nil
	case "r2":
		location := strings.TrimSpace(cfg.Location)
		if location == "" {
			location = DefaultR2Bucket
		}
		bucket, err := ParseR2Bucket(location)
		if err != nil {
			return Address{}, err
		}
		return Address{Type: typ, Location: bucket.String()}, nil
	default:
		return Address{}, fmt.Errorf("unsupported Archive type %q", typ)
	}
}

func ConfigFromAddress(addr Address) model.ArchiveConfig {
	return model.ArchiveConfig{Type: addr.Type, Location: addr.Location}
}

func safeCatalogComponent(machine string) string {
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "machine"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(machine) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" || out == "." || out == ".." {
		sum := sha256.Sum256([]byte(machine))
		return "machine-" + hex.EncodeToString(sum[:8])
	}
	return out
}

func expandLocalRoot(root string) (string, error) {
	if root == "" {
		root = "~/.aha/archive"
	}
	return paths.Expand(root)
}

type marker struct {
	Schema           string   `json:"schema"`
	DepotID          string   `json:"depot_id"`
	Layout           string   `json:"layout"`
	FormatMajor      int      `json:"format_major,omitempty"`
	FormatMinor      int      `json:"format_minor,omitempty"`
	RequiredFeatures []string `json:"required_features,omitempty"`
	OptionalFeatures []string `json:"optional_features,omitempty"`
	CreatedAt        string   `json:"created_at"`
	CreatedBy        string   `json:"created_by"`
}
