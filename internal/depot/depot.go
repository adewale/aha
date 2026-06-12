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
	DefaultR2Bucket = "aha-depot"
)

type Address struct {
	Type     string
	Location string
}

type VerifyReport struct {
	Manifests       int      `json:"manifests"`
	Machines        int      `json:"machines"`
	Repaired        bool     `json:"repaired"`
	Deep            bool     `json:"deep"`
	BytesRead       int64    `json:"bytes_read"`
	BytesDownloaded int64    `json:"bytes_downloaded"`
	Problems        []string `json:"problems,omitempty"`
}

func ParseAddress(s string) (Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Address{Type: "local", Location: "~/.aha/depot"}, nil
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
		return Address{}, fmt.Errorf("local depot path required")
	}
	if typ != "local" && typ != "r2" {
		return Address{}, fmt.Errorf("unsupported depot type %q", typ)
	}
	return Address{Type: typ, Location: loc}, nil
}

func AddressFromConfig(cfg model.DepotConfig) Address {
	if cfg.Type == "" {
		return Address{Type: "local", Location: "~/.aha/depot"}
	}
	loc := cfg.Location
	if cfg.Type == "r2" && loc == "" {
		loc = DefaultR2Bucket
	}
	return Address{Type: cfg.Type, Location: loc}
}

func ConfigFromAddress(addr Address) model.DepotConfig {
	return model.DepotConfig{Type: addr.Type, Location: addr.Location}
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
		root = "~/.aha/depot"
	}
	return paths.Expand(root)
}

type marker struct {
	Schema    string `json:"schema"`
	DepotID   string `json:"depot_id"`
	Layout    string `json:"layout"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}
