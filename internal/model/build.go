package model

import "strconv"

// Linker-injected build metadata. Release builds set these with -ldflags -X;
// development fallbacks remain explicit so a binary never masquerades as an
// unidentified release checkout.
var (
	BuildCommit = "development"
	BuildTime   = "unknown"
	BuildDirty  = "true"
)

type BuildIdentity struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
	Dirty   bool   `json:"dirty"`
}

func RunningBuild() BuildIdentity {
	dirty, err := strconv.ParseBool(BuildDirty)
	if err != nil {
		dirty = true
	}
	return BuildIdentity{Version: Version, Commit: BuildCommit, BuiltAt: BuildTime, Dirty: dirty}
}
