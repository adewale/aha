package model

const Version = "0.1.0"
const BundleSchema = "agent-session-snapshot-bundle/v1"

type Config struct {
	MachineID            string         `json:"machine_id"`
	MachineLabel         string         `json:"machine_label,omitempty"`
	Sources              []SourceConfig `json:"sources"`
	CorpusDir            string         `json:"corpus_dir"`
	BundleOutDir         string         `json:"bundle_out_dir"`
	PathMode             string         `json:"path_mode"`
	IncludeSubagents     bool           `json:"include_subagents"`
	IncludeImages        bool           `json:"include_images"`
	IndexToolOutput      bool           `json:"index_tool_output"`
	Redaction            string         `json:"redaction"`
	AcceptSecretsWarning bool           `json:"accept_secrets_warning"`
}

type SourceConfig struct {
	Type    string `json:"type"`
	Root    string `json:"root"`
	Enabled bool   `json:"enabled"`
}

type DefaultRoot struct{ OS, Path string }

type AdapterCapabilities struct {
	HasThreads        bool `json:"has_threads"`
	HasSubagents      bool `json:"has_subagents"`
	HasImages         bool `json:"has_images"`
	HasToolCalls      bool `json:"has_tool_calls"`
	HasStableEntryIDs bool `json:"has_stable_entry_ids"`
	CanLinkSubagents  bool `json:"can_link_subagents"`
}

type SessionFile struct {
	Source       string
	Root         string
	Path         string
	RelativePath string
	SessionID    string
	CWD          string
	StartedAt    string
	IsSubagent   bool
}

type ArtifactFile struct {
	Source       string
	Root         string
	Path         string
	RelativePath string
	Kind         string
	ParentHint   string
}

type ParsedSession struct {
	Source          string
	SourceSessionID string
	CWD             string
	StartedAt       string
	IsSubagent      bool
	Entries         []ParsedEntry
	Diagnostics     []string
	Metadata        map[string]any
}

type ParsedEntry struct {
	EntryID   string
	ParentID  string
	LineNo    int
	EntryType string
	Timestamp string
	Role      string
	RawJSON   string
	Text      string
	ToolName  string
	Command   string
	FilesJSON string
	Model     string
	Provider  string
	Tokens    int64
	Cost      float64
	Assets    []ParsedAsset
	Metadata  map[string]any
}

type ParsedAsset struct {
	AssetKind    string
	ContentIndex int
	PromptOrder  int
	RawRef       string
	MimeType     string
	Data         []byte
	Width        int
	Height       int
	Metadata     map[string]any
}

type Manifest struct {
	Schema         string          `json:"schema"`
	BundleID       string          `json:"bundle_id"`
	MachineID      string          `json:"machine_id"`
	MachineLabel   string          `json:"machine_label,omitempty"`
	CapturedAt     string          `json:"captured_at"`
	CreatedBy      string          `json:"created_by"`
	Implementation Implementation  `json:"implementation"`
	Source         ManifestSource  `json:"source"`
	Policy         ManifestPolicy  `json:"policy"`
	Counts         ManifestCounts  `json:"counts"`
	Adapters       []ManifestAdapt `json:"adapters"`
	Files          []ManifestFile  `json:"files"`
}

type Implementation struct {
	Language string `json:"language"`
	Archive  string `json:"archive"`
}

type ManifestSource struct {
	HostOS       string `json:"host_os"`
	HostnameHash string `json:"hostname_hash,omitempty"`
	UserHash     string `json:"user_hash,omitempty"`
}

type ManifestPolicy struct {
	PathMode         string `json:"path_mode"`
	IncludeSubagents bool   `json:"include_subagents"`
	IncludeImages    bool   `json:"include_images"`
	IndexToolOutput  bool   `json:"index_tool_output"`
	Redaction        string `json:"redaction"`
}

type ManifestCounts struct {
	SessionFiles      int   `json:"session_files"`
	ArtifactFiles     int   `json:"artifact_files"`
	ImageFiles        int   `json:"image_files"`
	BytesUncompressed int64 `json:"bytes_uncompressed"`
}

type ManifestAdapt struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Capabilities AdapterCapabilities `json:"capabilities"`
}

type ManifestFile struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	RawPath      string `json:"raw_path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	SessionID    string `json:"session_id,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	Entries      int    `json:"entries,omitempty"`
	CopyState    string `json:"copy_state"`
	IsSubagent   bool   `json:"is_subagent,omitempty"`
	ParentHint   string `json:"parent_hint,omitempty"`
}

type CapturedFile struct {
	Manifest ManifestFile
	Data     []byte
	Path     string
}
