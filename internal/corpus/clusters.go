package corpus

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/redact"
)

// This file turns paired tool calls into error clusters. The pipeline is:
//
//  1. Adapters project every tool_use and tool_result block into slice fields
//     on ParsedEntry. A single transcript entry can contain many tool calls.
//  2. BuildToolInvocations pairs calls to results by tool_use_id (or encounter
//     order when ids are absent) and emits one invocation per call.
//  3. Clusters groups failing invocations by (tool_name, command_family,
//     error_signature) and ranks recurring failures as candidate skills.
//
// The stored/displayed samples are normalized signatures, not raw tool output.
// That keeps clusters useful without creating a new bypass around
// index_tool_output=false.

const MaxClusterLimit = 200

var (
	reHexLong   = regexp.MustCompile(`\b[0-9a-f]{7,}\b`)
	reUUID      = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reNumber    = regexp.MustCompile(`\b\d+\b`)
	reQuoted    = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	rePath      = regexp.MustCompile(`(/[^\s'"]+)+`)
	reWSRun     = regexp.MustCompile(`\s+`)
	reURL       = regexp.MustCompile(`https?://[^\s'"]+`)
	reArgToken  = regexp.MustCompile(`^-`)
	reLeadingCD = regexp.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*cd\s+\S+\s+&&\s*(.+)$`)

	clusterSignatureRedactor = redact.NewDefault()
)

// normalizeErrorSignature collapses a tool_result error body into a stable,
// privacy-preserving fingerprint. URLs, paths, quoted strings, UUIDs, long
// hex strings, numbers, and known secret shapes are replaced with placeholders
// so the same failure shape across repos/PRs/ids collapses to one cluster.
func normalizeErrorSignature(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s, _ = clusterSignatureRedactor.Apply(s)
	s = strings.ToLower(s)
	s = reURL.ReplaceAllString(s, "<url>")
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = rePath.ReplaceAllString(s, "<path>")
	s = reQuoted.ReplaceAllString(s, "<str>")
	s = reHexLong.ReplaceAllString(s, "<hex>")
	s = reNumber.ReplaceAllString(s, "<n>")
	s = reWSRun.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// commandFamily reduces a shell command (or, when absent, a tool name) to a
// coarse family used as a clustering key: e.g. `gh pr create --title "x"` ->
// "gh pr create", `git push origin foo` -> "git push". It keeps the program
// plus up to two leading non-flag subcommand tokens, skipping leading env
// assignments so secrets passed as ENV=... do not become cluster keys.
func commandFamily(toolName, command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return strings.TrimSpace(toolName)
	}
	if redacted, _ := clusterSignatureRedactor.Apply(cmd); redacted != "" {
		cmd = redacted
	}
	// Skip the common agent shell wrapper `cd <repo> && ...` so clusters key
	// on the failing command, not on `cd`.
	if m := reLeadingCD.FindStringSubmatch(cmd); m != nil {
		cmd = m[1]
	}
	// Take the first line / first statement only.
	if i := strings.IndexAny(cmd, "\n|;"); i >= 0 {
		cmd = cmd[:i]
	}
	if i := strings.Index(cmd, " & "); i >= 0 {
		cmd = cmd[:i]
	}
	fields := strings.Fields(cmd)
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return strings.TrimSpace(toolName)
	}
	out := []string{fields[0]}
	for _, f := range fields[1:] {
		if len(out) >= 3 {
			break
		}
		if reArgToken.MatchString(f) {
			break
		}
		// Stop at obvious operands (paths, urls, assignments).
		if strings.ContainsAny(f, "/=$") || strings.Contains(f, "://") {
			break
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// BuildToolInvocations pairs tool_use entries with their tool_result siblings
// within a parsed session and returns one ToolInvocation per call. Outcome
// fields are filled from the paired result when present.
func BuildToolInvocations(entries []model.ParsedEntry, projectKey, machineID string) []ToolInvocation {
	type outcome struct {
		isError       bool
		text          string
		exitCode      int64
		exitCodeValid bool
	}
	resultsByID := map[string]outcome{}
	var unkeyedResults []outcome
	for _, e := range entries {
		for _, r := range e.ToolResults {
			out := outcome{isError: r.IsError, text: r.OutcomeText, exitCode: r.ExitCode, exitCodeValid: r.ExitCodeValid}
			if r.ForID != "" {
				if _, seen := resultsByID[r.ForID]; !seen {
					resultsByID[r.ForID] = out
				}
				continue
			}
			unkeyedResults = append(unkeyedResults, out)
		}
	}
	var calls []struct {
		entry model.ParsedEntry
		call  model.ParsedToolCall
	}
	for _, e := range entries {
		if len(e.ToolCalls) == 0 && len(e.ToolResults) == 0 && e.Role != "toolResult" && e.Role != "bashExecution" && (e.ToolName != "" || e.Command != "") {
			calls = append(calls, struct {
				entry model.ParsedEntry
				call  model.ParsedToolCall
			}{entry: e, call: model.ParsedToolCall{ToolName: e.ToolName, Command: e.Command, FilesJSON: e.FilesJSON, Ordinal: 0}})
			continue
		}
		for _, c := range e.ToolCalls {
			calls = append(calls, struct {
				entry model.ParsedEntry
				call  model.ParsedToolCall
			}{entry: e, call: c})
		}
	}
	var out []ToolInvocation
	unkeyedIndex := 0
	for _, item := range calls {
		c := item.call
		if c.ToolName == "" && c.Command == "" && c.FilesJSON == "" {
			continue
		}
		toolKey := c.ID
		if toolKey == "" {
			toolKey = fmt.Sprintf("#%03d", c.Ordinal)
		}
		inv := ToolInvocation{
			SessionKey:     "", // filled by caller (needs session key)
			EntryID:        item.entry.EntryID,
			ToolKey:        toolKey,
			ToolUseID:      c.ID,
			Ordinal:        c.Ordinal,
			ToolName:       c.ToolName,
			CommandFamily:  commandFamily(c.ToolName, c.Command),
			Command:        commandFamily(c.ToolName, c.Command),
			Timestamp:      item.entry.Timestamp,
			ProjectKey:     projectKey,
			MachineID:      machineID,
			ExitCodeValid:  false,
			ErrorSignature: "",
		}
		var res outcome
		var ok bool
		if c.ID != "" {
			res, ok = resultsByID[c.ID]
		} else if unkeyedIndex < len(unkeyedResults) {
			res, ok = unkeyedResults[unkeyedIndex], true
			unkeyedIndex++
		}
		if ok {
			inv.OutcomeObserved = true
			inv.IsError = res.isError
			inv.ExitCode = res.exitCode
			inv.ExitCodeValid = res.exitCodeValid
			if res.isError {
				inv.OutcomeText = res.text
				inv.ErrorSignature = normalizeErrorSignature(res.text)
			}
		}
		out = append(out, inv)
	}
	return out
}

// ToolInvocation is one paired tool call ready for insertion.
type ToolInvocation struct {
	SessionKey      string
	EntryID         string
	ToolKey         string
	ToolUseID       string
	Ordinal         int
	ToolName        string
	CommandFamily   string
	Command         string
	ExitCode        int64
	ExitCodeValid   bool
	IsError         bool
	ErrorSignature  string
	OutcomeText     string
	OutcomeObserved bool
	Timestamp       string
	ProjectKey      string
	MachineID       string
}

func fallbackErrorSignature(inv ToolInvocation) string {
	if inv.ExitCodeValid {
		return fmt.Sprintf("exit_code:%d", inv.ExitCode)
	}
	return "tool_error"
}

// clusterScore favors signals that recur AND spread: a single noisy session
// shouldn't outrank one hit across many sessions/projects. `weight` is the
// recurrence count being scored — failing-invocation count for error clusters,
// resolved-episode count for skill candidates.
func clusterScore(weight, sessions, projects int) float64 {
	if weight <= 0 {
		return 0
	}
	return float64(weight) * spread(sessions, projects)
}

func sortToolInvocations(invs []ToolInvocation) {
	sort.Slice(invs, func(i, j int) bool {
		if invs[i].EntryID != invs[j].EntryID {
			return invs[i].EntryID < invs[j].EntryID
		}
		if invs[i].Ordinal != invs[j].Ordinal {
			return invs[i].Ordinal < invs[j].Ordinal
		}
		return invs[i].ToolKey < invs[j].ToolKey
	})
}

// messageRefText builds a canonical msg ref so a dashboard cluster can drill
// straight into the failing command via the existing read tool.
func messageRefText(sessionKey, entryID string) (string, error) {
	sk, err := model.ParseSessionKey(sessionKey)
	if err != nil {
		return "", err
	}
	eid, err := model.NewEntryID(entryID)
	if err != nil {
		return "", err
	}
	return model.FormatRef(model.MessageRef{Session: sk, Entry: eid}), nil
}
