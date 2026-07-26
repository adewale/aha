package adapters

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/adewale/aha/internal/model"
)

// VersionSignal reports Claude Code's producer version, written at the top
// level of the records that carry it as a string such as "2.1.92".
func (ClaudeCode) VersionSignal(entry model.ParsedEntry) model.VersionSignal {
	return model.ProducerVersion(rawScalarField(entry.RawJSON, "version"))
}

// VersionSignal reports the Codex CLI's producer version, written inside the
// session_meta envelope of modern rollouts as payload.cli_version. Older flat
// rollouts carry no version, so the value is empty for every record in them.
func (CodexCLI) VersionSignal(entry model.ParsedEntry) model.VersionSignal {
	return model.ProducerVersion(rawScalarField(entry.RawJSON, "payload", "cli_version"))
}

// VersionSignal reports Pi's on-disk schema revision from its session header,
// an integer such as 3. This is the format revision, not the version of Pi:
// it moves when the file layout changes, so it cannot stand in for a producer
// version and is declared as VersionKindSchema.
func (Pi) VersionSignal(entry model.ParsedEntry) model.VersionSignal {
	return model.SchemaVersion(rawScalarField(entry.RawJSON, "version"))
}

// VersionSignal declares that OpenCode carries no version. Sessions are read
// out of OpenCode's SQLite database and exported to JSONL; neither the rows
// nor the export envelope record a producer or schema version, so coverage
// for this source is known to be unstratified.
func (OpenCode) VersionSignal(entry model.ParsedEntry) model.VersionSignal {
	return model.NoVersion()
}

// rawScalarField walks a dotted path through a record's raw line and renders
// the scalar it finds exactly as the producer wrote it. Numbers are decoded as
// json.Number so an integer schema revision stays "3" instead of arriving via
// float64 as "3e+00" or similar; anything that is not a scalar, and any path
// that does not resolve, yields "".
func rawScalarField(raw string, path ...string) string {
	if strings.TrimSpace(raw) == "" || len(path) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v, ok = m[key]
		if !ok {
			return ""
		}
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}
