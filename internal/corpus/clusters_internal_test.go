package corpus

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestNormalizeErrorSignatureCollapsesVariableParts(t *testing.T) {
	a := normalizeErrorSignature(`failed to create PR #4821 for sha 9af3c1b2e: see https://github.com/o/r/pull/4821`)
	b := normalizeErrorSignature(`failed to create PR #17 for sha deadbeef99: see https://github.com/x/y/pull/17`)
	if a != b {
		t.Fatalf("signatures should collapse to the same shape:\n a=%q\n b=%q", a, b)
	}
	if a == "" {
		t.Fatal("signature unexpectedly empty")
	}
}

func TestNormalizeErrorSignatureRedactsSecretShapes(t *testing.T) {
	sig := normalizeErrorSignature(`Authorization: Bearer abc123def4567890 failed`)
	if sig == "" || sig == strings.ToLower(`Authorization: Bearer abc123def4567890 failed`) {
		t.Fatalf("signature did not normalize/redact bearer token: %q", sig)
	}
	if strings.Contains(sig, "abc123def4567890") {
		t.Fatalf("signature leaked bearer token: %q", sig)
	}
}

func TestNormalizeErrorSignatureDistinguishesDifferentErrors(t *testing.T) {
	perm := normalizeErrorSignature(`GraphQL: you do not have permission to execute CreateRepository`)
	field := normalizeErrorSignature(`unknown JSON field: defaultBranch`)
	if perm == field {
		t.Fatalf("distinct errors collapsed: %q", perm)
	}
}

func TestCommandFamily(t *testing.T) {
	cases := []struct {
		tool, command, want string
	}{
		{"Bash", `gh pr create --title "x" --body "y"`, "gh pr create"},
		{"Bash", "git push origin feature/foo", "git push origin"},
		{"Bash", "git push", "git push"},
		{"Bash", "gh api -X POST /repos/o/r/issues", "gh api"},
		{"Bash", "ls -la /tmp", "ls"},
		{"Read", "", "Read"},
		{"Bash", "cd /home/user/x && go test ./...", "go test"},
		{"Bash", "AWS_SECRET_ACCESS_KEY=abc npm test", "npm test"},
	}
	for _, c := range cases {
		if got := commandFamily(c.tool, c.command); got != c.want {
			t.Errorf("commandFamily(%q,%q)=%q want %q", c.tool, c.command, got, c.want)
		}
	}
}

func TestBuildToolInvocationsPairsResults(t *testing.T) {
	entries := []model.ParsedEntry{
		{EntryID: "e1", Timestamp: "2026-01-01T00:00:00Z", ToolCalls: []model.ParsedToolCall{{ID: "tu_1", ToolName: "Bash", Command: "gh pr create --title x"}}},
		{EntryID: "e2", Role: "toolResult", ToolResults: []model.ParsedToolResult{{ForID: "tu_1", IsError: true, OutcomeText: "Head sha can't be blank"}}},
		{EntryID: "e3", ToolCalls: []model.ParsedToolCall{{ID: "tu_2", ToolName: "Bash", Command: "gh pr view 1"}}},
		{EntryID: "e4", Role: "toolResult", ToolResults: []model.ParsedToolResult{{ForID: "tu_2", IsError: false, OutcomeText: "ok"}}},
	}
	invs := BuildToolInvocations(entries, "proj", "m1")
	if len(invs) != 2 {
		t.Fatalf("want 2 invocations, got %d", len(invs))
	}
	byID := map[string]ToolInvocation{}
	for _, inv := range invs {
		byID[inv.ToolUseID] = inv
	}
	if got := byID["tu_1"]; !got.IsError || got.ErrorSignature == "" || got.CommandFamily != "gh pr create" {
		t.Fatalf("tu_1 not paired as error: %+v", got)
	}
	if got := byID["tu_2"]; got.IsError {
		t.Fatalf("tu_2 should be success: %+v", got)
	}
}

func TestBuildToolInvocationsMarksUnresolvedCallsPending(t *testing.T) {
	entries := []model.ParsedEntry{{EntryID: "call", ToolCalls: []model.ParsedToolCall{{ID: "tu_1", ToolName: "bash", Command: "go test"}}}}
	invs := BuildToolInvocations(entries, "proj", "m1")
	if len(invs) != 1 {
		t.Fatalf("want pending invocation for in-memory pairing semantics, got %+v", invs)
	}
	if invs[0].OutcomeObserved {
		t.Fatalf("unpaired call should be marked pending, got %+v", invs[0])
	}
}

func TestBuildToolInvocationsDoesNotTreatToolResultMetadataAsCall(t *testing.T) {
	entries := []model.ParsedEntry{{
		EntryID: "result", Role: "toolResult", ToolName: "bash",
		ToolResults: []model.ParsedToolResult{{ForID: "missing", IsError: true, OutcomeText: "raw tool output"}},
	}}
	if invs := BuildToolInvocations(entries, "proj", "m1"); len(invs) != 0 {
		t.Fatalf("tool-result metadata should not become a synthetic call: %+v", invs)
	}
}

func TestBuildToolInvocationsPairsLegacySingleCallByOrder(t *testing.T) {
	entries := []model.ParsedEntry{
		{EntryID: "legacy-call", Timestamp: "2026-01-01T00:00:00Z", ToolName: "Bash", Command: "go test ./..."},
		{EntryID: "legacy-result", Role: "toolResult", ToolResults: []model.ParsedToolResult{{IsError: true, OutcomeText: "FAIL github.com/example/pkg"}}},
	}
	invs := BuildToolInvocations(entries, "proj", "m1")
	if len(invs) != 1 {
		t.Fatalf("want 1 invocation, got %d: %+v", len(invs), invs)
	}
	if !invs[0].IsError || invs[0].ErrorSignature == "" || invs[0].CommandFamily != "go test" {
		t.Fatalf("legacy single call was not paired with following unkeyed result: %+v", invs[0])
	}
}

func TestBuildToolInvocationsKeepsMultipleCallsInOneEntry(t *testing.T) {
	entries := []model.ParsedEntry{
		{EntryID: "assistant", Timestamp: "2026-01-01T00:00:00Z", ToolCalls: []model.ParsedToolCall{
			{ID: "tu_1", ToolName: "Bash", Command: "go test ./...", Ordinal: 0},
			{ID: "tu_2", ToolName: "Bash", Command: "gh pr create --title x", Ordinal: 1},
		}},
		{EntryID: "results", Role: "toolResult", ToolResults: []model.ParsedToolResult{
			{ForID: "tu_1", IsError: true, OutcomeText: "go test failed", Ordinal: 0},
			{ForID: "tu_2", IsError: true, OutcomeText: "head sha blank", Ordinal: 1},
		}},
	}
	invs := BuildToolInvocations(entries, "proj", "m1")
	if len(invs) != 2 {
		t.Fatalf("want 2 invocations, got %d: %+v", len(invs), invs)
	}
	byID := map[string]ToolInvocation{}
	for _, inv := range invs {
		byID[inv.ToolUseID] = inv
	}
	if byID["tu_1"].CommandFamily != "go test" || byID["tu_2"].CommandFamily != "gh pr create" {
		t.Fatalf("tool calls/results cross-contaminated: %+v", invs)
	}
}

func TestClusterScoreRewardsSpread(t *testing.T) {
	concentrated := clusterScore(10, 1, 1) // 10 hits, all one session
	spread := clusterScore(10, 10, 5)      // 10 hits across 10 sessions/5 projects
	if spread <= concentrated {
		t.Fatalf("spread score %f should exceed concentrated %f", spread, concentrated)
	}
}
