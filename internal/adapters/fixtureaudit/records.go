package fixtureaudit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// maxFixtureLineBytes bounds a single fixture line. Vendored corpora carry
// base64 images inline, so the default scanner buffer is far too small.
const maxFixtureLineBytes = 8 * 1024 * 1024

// Record is one non-empty line of a fixture. Raw is kept alongside the decoded
// value because a version signal is read from exactly the bytes the producer
// wrote, not from a re-encoding of them.
type Record struct {
	LineNo int
	Raw    string
	Value  any
}

// ReadRecords decodes every non-blank line of the JSONL file at path.
func ReadRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1024*1024), maxFixtureLineBytes)
	lineNo := 0
	for s.Scan() {
		lineNo++
		raw := s.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		out = append(out, Record{LineNo: lineNo, Raw: raw, Value: v})
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// KeyPaths returns every distinct dotted key-path in v, sorted. Array steps
// are marked '[]'.
func KeyPaths(v any) []string {
	seen := map[string]struct{}{}
	walkKeyPaths(v, "", seen)
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func walkKeyPaths(v any, prefix string, out map[string]struct{}) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			if prefix != "" {
				out[prefix] = struct{}{}
			}
			return
		}
		for k, child := range x {
			next := k
			if prefix != "" {
				next = prefix + "." + k
			}
			walkKeyPaths(child, next, out)
		}
	case []any:
		if len(x) == 0 {
			if prefix != "" {
				out[prefix+"[]"] = struct{}{}
			}
			return
		}
		for _, child := range x {
			walkKeyPaths(child, prefix+"[]", out)
		}
	default:
		if prefix != "" {
			out[prefix] = struct{}{}
		}
	}
}
