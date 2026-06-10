package depot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

// Depot v2 layout (docs/depot-v2-spec.md). These constants and key builders
// are the locked pre-release contract, pinned by TestV2ObjectKeyGoldens:
//
//	aha-depot/v2                                  marker
//	blobs/v2/<sha256>.zst                         one compressed file version, write-once
//	machines/index.json                           registry of machine namespaces
//	machines/<machine>/manifests/<sha256>.json    one snapshot manifest, write-once
//	machines/<machine>/latest                     pointer, conditional PUT
const (
	MarkerSchemaV2      = "aha-depot/v2"
	LayoutVersionV2     = "v2"
	LatestPointerSchema = "aha-depot-latest/v2"
	MachinesIndexSchema = "aha-depot-machines/v2"
	MachinesIndexKey    = "machines/index.json"
	MarkerObjectKey     = "aha-depot.json"
)

// BlobObjectKey is the object key for a stored file version. The key is
// derived from a validated BlobKey, so an unsafe or non-content-addressed
// blob key cannot be constructed.
func BlobObjectKey(k model.BlobKey) string {
	return "blobs/v2/" + k.String() + ".zst"
}

// ManifestObjectKey is the object key for one snapshot manifest in a
// machine's namespace.
func ManifestObjectKey(machine string, sha model.ManifestSHA256) string {
	return machinePrefix(machine) + "manifests/" + sha.String() + ".json"
}

// LatestPointerKey is the object key of a machine's latest-snapshot pointer.
func LatestPointerKey(machine string) string {
	return machinePrefix(machine) + "latest"
}

// machinePrefix is the only place v2 machine namespaces are constructed.
// Machine IDs are sanitized with the same component rules as the v1
// catalog, so a hostile machine ID cannot escape machines/<id>/.
func machinePrefix(machine string) string {
	return "machines/" + safeCatalogComponent(machine) + "/"
}

type latestPointer struct {
	Schema         string `json:"schema"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// EncodeLatestPointer serializes a machine's latest-snapshot pointer.
func EncodeLatestPointer(sha model.ManifestSHA256) ([]byte, error) {
	if !sha.Valid() {
		return nil, fmt.Errorf("invalid manifest sha for latest pointer")
	}
	b, err := json.Marshal(latestPointer{Schema: LatestPointerSchema, ManifestSHA256: sha.String()})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// DecodeLatestPointer parses and validates a latest-snapshot pointer.
func DecodeLatestPointer(b []byte) (model.ManifestSHA256, error) {
	var p latestPointer
	if err := json.Unmarshal(b, &p); err != nil {
		return model.ManifestSHA256{}, fmt.Errorf("invalid latest pointer: %w", err)
	}
	if p.Schema != LatestPointerSchema {
		return model.ManifestSHA256{}, fmt.Errorf("invalid latest pointer schema %q", p.Schema)
	}
	sha, err := model.NewManifestSHA256(p.ManifestSHA256)
	if err != nil {
		return model.ManifestSHA256{}, fmt.Errorf("invalid latest pointer: %w", err)
	}
	return sha, nil
}

type machinesIndex struct {
	Schema   string   `json:"schema"`
	Machines []string `json:"machines"`
}

// EncodeMachinesIndex serializes the registry of machine namespaces,
// sorted and deduplicated. The index is what lets pull discover machines
// with a single GET instead of a LIST (invariant I6).
func EncodeMachinesIndex(machines []string) ([]byte, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(machines))
	for _, m := range machines {
		if strings.TrimSpace(m) == "" {
			return nil, fmt.Errorf("invalid machines index: empty machine id")
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	b, err := json.Marshal(machinesIndex{Schema: MachinesIndexSchema, Machines: out})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// DecodeMachinesIndex parses and validates the machine registry.
func DecodeMachinesIndex(b []byte) ([]string, error) {
	var idx machinesIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("invalid machines index: %w", err)
	}
	if idx.Schema != MachinesIndexSchema {
		return nil, fmt.Errorf("invalid machines index schema %q", idx.Schema)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(idx.Machines))
	for _, m := range idx.Machines {
		if strings.TrimSpace(m) == "" {
			return nil, fmt.Errorf("invalid machines index: empty machine id")
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out, nil
}
