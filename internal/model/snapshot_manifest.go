package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// SnapshotManifestSchema identifies the depot v2 snapshot manifest format
// (docs/depot-v2-spec.md). A snapshot manifest is the logically-full
// description of one machine's captured state: every session/artefact file
// version, each addressed by the SHA-256 of its content. The manifest's own
// SHA-256 over its canonical encoding is the snapshot identity.
const SnapshotManifestSchema = "aha-snapshot-manifest/v2"

// MaxSnapshotFiles caps the file list, mirroring the v1 bundle manifest cap.
const MaxSnapshotFiles = 200000

// SnapshotManifest is the depot v2 snapshot artefact. It reuses
// ManifestFile so the corpus ingest path is shared with v1 bundle import;
// the Entries hint is never set by v2 capture (entry counts are
// ingest-derived corpus facts, not capture-time parses).
type SnapshotManifest struct {
	Schema       string          `json:"schema"`
	MachineID    string          `json:"machine_id"`
	MachineLabel string          `json:"machine_label,omitempty"`
	CapturedAt   string          `json:"captured_at"`
	CreatedBy    string          `json:"created_by"`
	Source       ManifestSource  `json:"source"`
	Policy       ManifestPolicy  `json:"policy"`
	Adapters     []ManifestAdapt `json:"adapters"`
	Files        []ManifestFile  `json:"files"`
}

// ManifestSHA256 is the identity of a snapshot manifest: the SHA-256 of its
// canonical encoding. Only EncodeSnapshotManifest/DecodeSnapshotManifest and
// NewManifestSHA256 produce valid values, so a malformed identity cannot
// circulate.
type ManifestSHA256 struct{ value string }

// BlobKey is the content address of a stored file version: the SHA-256 of
// the uncompressed file bytes. Distinct from ManifestSHA256 so the two
// address spaces cannot be confused at compile time.
type BlobKey struct{ value string }

func NewManifestSHA256(s string) (ManifestSHA256, error) {
	if _, err := ParseSHA256Hex(s); err != nil {
		return ManifestSHA256{}, fmt.Errorf("invalid manifest sha256: %w", err)
	}
	return ManifestSHA256{value: s}, nil
}

func NewBlobKey(s string) (BlobKey, error) {
	if _, err := ParseSHA256Hex(s); err != nil {
		return BlobKey{}, fmt.Errorf("invalid blob key: %w", err)
	}
	return BlobKey{value: s}, nil
}

func (v ManifestSHA256) String() string { return v.value }
func (v BlobKey) String() string        { return v.value }
func (v ManifestSHA256) Valid() bool    { return len(v.value) == 64 && isLowerHex(v.value) }
func (v BlobKey) Valid() bool           { return len(v.value) == 64 && isLowerHex(v.value) }

// SnapshotStateSHA256 digests the capture-time-invariant state of a
// manifest: everything except when it was captured and by which build.
// Two snapshots of an unchanged machine on different days have different
// identities (provenance) but equal state digests, which is what lets a
// push recognise "nothing changed" from the parent pointer alone.
func SnapshotStateSHA256(m SnapshotManifest) (SHA256Hex, error) {
	m.CapturedAt = "state"
	m.CreatedBy = "state"
	b, _, err := EncodeSnapshotManifest(m)
	if err != nil {
		return SHA256Hex{}, err
	}
	sum := sha256.Sum256(b)
	return SHA256Hex{value: hex.EncodeToString(sum[:])}, nil
}

// EncodeSnapshotManifest validates m, normalizes it (files sorted by
// relative path), and returns the canonical bytes plus their SHA-256
// identity. Invalid manifests cannot be encoded, so they can never acquire
// an identity or reach a depot.
func EncodeSnapshotManifest(m SnapshotManifest) ([]byte, ManifestSHA256, error) {
	m.Files = append([]ManifestFile(nil), m.Files...)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].RelativePath < m.Files[j].RelativePath })
	if err := validateSnapshotManifest(m); err != nil {
		return nil, ManifestSHA256{}, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, ManifestSHA256{}, err
	}
	b = append(b, '\n')
	sum := sha256.Sum256(b)
	return b, ManifestSHA256{value: hex.EncodeToString(sum[:])}, nil
}

// DecodeSnapshotManifest parses canonical snapshot manifest bytes and
// returns the manifest plus its identity. Only canonical bytes are
// accepted: re-encoding the decoded manifest must reproduce the input
// exactly, so one logical manifest has exactly one identity.
func DecodeSnapshotManifest(b []byte) (SnapshotManifest, ManifestSHA256, error) {
	var m SnapshotManifest
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&m); err != nil {
		return SnapshotManifest{}, ManifestSHA256{}, fmt.Errorf("invalid snapshot manifest: %w", err)
	}
	if dec.More() {
		return SnapshotManifest{}, ManifestSHA256{}, fmt.Errorf("invalid snapshot manifest: trailing data")
	}
	canonical, sha, err := EncodeSnapshotManifest(m)
	if err != nil {
		return SnapshotManifest{}, ManifestSHA256{}, err
	}
	if !bytes.Equal(canonical, b) {
		return SnapshotManifest{}, ManifestSHA256{}, fmt.Errorf("invalid snapshot manifest: not in canonical encoding")
	}
	return m, sha, nil
}

func validateSnapshotManifest(m SnapshotManifest) error {
	if m.Schema != SnapshotManifestSchema {
		return fmt.Errorf("invalid snapshot manifest: schema %q, want %q", m.Schema, SnapshotManifestSchema)
	}
	if strings.TrimSpace(m.MachineID) == "" {
		return fmt.Errorf("invalid snapshot manifest: machine_id required")
	}
	if strings.TrimSpace(m.CapturedAt) == "" {
		return fmt.Errorf("invalid snapshot manifest: captured_at required")
	}
	if len(m.Files) > MaxSnapshotFiles {
		return fmt.Errorf("invalid snapshot manifest: too many files: %d", len(m.Files))
	}
	prev := ""
	for i, mf := range m.Files {
		if err := ValidateSourceDataPath(mf.RelativePath); err != nil {
			return fmt.Errorf("invalid snapshot manifest: %w", err)
		}
		if i > 0 && mf.RelativePath <= prev {
			return fmt.Errorf("invalid snapshot manifest: duplicate file path %s", mf.RelativePath)
		}
		prev = mf.RelativePath
		if strings.TrimSpace(mf.Source) == "" {
			return fmt.Errorf("invalid snapshot manifest: file source required for %s", mf.RelativePath)
		}
		switch mf.Kind {
		case "session":
			want := "sources/" + mf.Source + "/sessions/"
			if !strings.HasPrefix(mf.RelativePath, want) {
				return fmt.Errorf("invalid snapshot manifest: session path %s must be under %s", mf.RelativePath, want)
			}
		case "artifact":
			want := "sources/" + mf.Source + "/artifacts/"
			if !strings.HasPrefix(mf.RelativePath, want) {
				return fmt.Errorf("invalid snapshot manifest: artefact path %s must be under %s", mf.RelativePath, want)
			}
		default:
			return fmt.Errorf("invalid snapshot manifest: unsupported file kind %q", mf.Kind)
		}
		if _, err := NewBlobKey(mf.SHA256); err != nil {
			return fmt.Errorf("invalid snapshot manifest: file %s: %w", mf.RelativePath, err)
		}
		if mf.Bytes < 0 {
			return fmt.Errorf("invalid snapshot manifest: negative size for %s", mf.RelativePath)
		}
	}
	return nil
}

// ValidateSourceDataPath rejects relative paths that could escape or shadow
// reserved names when used as archive entries or object-key components.
// Shared by the v1 bundle reader/writer and the v2 snapshot manifest.
func ValidateSourceDataPath(name string) error {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasPrefix(name, "./") || strings.Contains(name, "/./") {
		return fmt.Errorf("unsafe archive path: %s", name)
	}
	if name == "manifest.json" || name == "checksums/sha256sums.txt" || strings.HasPrefix(name, "checksums/") {
		return fmt.Errorf("unsafe archive path: %s", name)
	}
	return nil
}
