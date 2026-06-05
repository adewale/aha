package corpus

import (
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// inv is a tiny helper to build a ToolInvocation with only the fields
// assembleEpisodes reads. Positional args keep the table cases compact.
func inv(session, entry string, ordinal int, family string, isError bool, ts string) ToolInvocation {
	return ToolInvocation{
		SessionKey:    session,
		EntryID:       entry,
		Ordinal:       ordinal,
		ToolName:      "Bash",
		CommandFamily: family,
		IsError:       isError,
		Timestamp:     ts,
	}
}

// ts builds an RFC3339 timestamp min minutes after a fixed base instant, so
// gap arithmetic in the cases below reads in real minutes.
func ts(min int) string {
	return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute).Format(time.RFC3339)
}

func TestDefaultEpisodeConfig(t *testing.T) {
	cfg := DefaultEpisodeConfig()
	if cfg.MaxGap != 30*time.Minute {
		t.Fatalf("MaxGap = %v, want 30m", cfg.MaxGap)
	}
	if cfg.MaxSteps != 40 {
		t.Fatalf("MaxSteps = %d, want 40", cfg.MaxSteps)
	}
}

func TestAssembleEpisodes(t *testing.T) {
	cfg := DefaultEpisodeConfig()

	tests := []struct {
		name string
		invs []ToolInvocation
		cfg  EpisodeConfig
		want []FailureEpisode
	}{
		{
			name: "empty input",
			invs: nil,
			cfg:  cfg,
			want: nil,
		},
		{
			name: "resolved simple",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "git push", false, ts(1)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e2",
					ResolutionPath: []string{"git push"},
					OpenedAt:       ts(0),
					ResolvedAt:     ts(1),
				},
			},
		},
		{
			name: "resolved with intervening other-family steps",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "git remote", false, ts(1)),
				inv("s1", "e3", 0, "git config", false, ts(2)),
				inv("s1", "e4", 0, "git push", false, ts(3)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e4",
					ResolutionPath: []string{"git remote", "git config", "git push"},
					OpenedAt:       ts(0),
					ResolvedAt:     ts(3),
				},
			},
		},
		{
			name: "abandoned by end of session",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "ls", false, ts(1)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:    "s1",
					OpenEntryID:   "e1",
					OpenOrdinal:   0,
					ToolName:      "Bash",
					CommandFamily: "git push",
					Resolved:      false,
					OpenedAt:      ts(0),
				},
			},
		},
		{
			name: "abandoned by gap",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "git push", false, ts(45)), // 45 min > 30 min gap
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:    "s1",
					OpenEntryID:   "e1",
					OpenOrdinal:   0,
					ToolName:      "Bash",
					CommandFamily: "git push",
					Resolved:      false,
					OpenedAt:      ts(0),
				},
			},
		},
		{
			name: "abandoned by step budget",
			invs: func() []ToolInvocation {
				out := []ToolInvocation{inv("s1", "e0", 0, "git push", true, ts(0))}
				// opener + resolver inclusive is 6 invocations, but MaxSteps=3.
				out = append(out, inv("s1", "e1", 0, "ls", false, ts(1)))
				out = append(out, inv("s1", "e2", 0, "ls", false, ts(2)))
				out = append(out, inv("s1", "e3", 0, "ls", false, ts(3)))
				out = append(out, inv("s1", "e4", 0, "ls", false, ts(4)))
				out = append(out, inv("s1", "e5", 0, "git push", false, ts(5)))
				return out
			}(),
			cfg: EpisodeConfig{MaxGap: 30 * time.Minute, MaxSteps: 3},
			want: []FailureEpisode{
				{
					SessionKey:    "s1",
					OpenEntryID:   "e0",
					OpenOrdinal:   0,
					ToolName:      "Bash",
					CommandFamily: "git push",
					Resolved:      false,
					OpenedAt:      ts(0),
				},
			},
		},
		{
			name: "two interleaved families no crosstalk",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "npm test", true, ts(1)),
				inv("s1", "e3", 0, "git push", false, ts(2)),
				inv("s1", "e4", 0, "npm test", false, ts(3)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e3",
					ResolutionPath: []string{"npm test", "git push"},
					OpenedAt:       ts(0),
					ResolvedAt:     ts(2),
				},
				{
					SessionKey:     "s1",
					OpenEntryID:    "e2",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "npm test",
					Resolved:       true,
					ResolveEntryID: "e4",
					ResolutionPath: []string{"git push", "npm test"},
					OpenedAt:       ts(1),
					ResolvedAt:     ts(3),
				},
			},
		},
		{
			name: "consecutive same-family failures collapse into one episode",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "git push", true, ts(1)),
				inv("s1", "e3", 0, "git push", true, ts(2)),
				inv("s1", "e4", 0, "git push", false, ts(3)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e4",
					ResolutionPath: []string{"git push", "git push", "git push"},
					OpenedAt:       ts(0),
					ResolvedAt:     ts(3),
				},
			},
		},
		{
			name: "unrelated success different family does not resolve",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "ls", false, ts(1)),
				inv("s1", "e3", 0, "echo", false, ts(2)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:    "s1",
					OpenEntryID:   "e1",
					OpenOrdinal:   0,
					ToolName:      "Bash",
					CommandFamily: "git push",
					Resolved:      false,
					OpenedAt:      ts(0),
				},
			},
		},
		{
			name: "multi-session grouping",
			invs: []ToolInvocation{
				inv("s2", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e1", 0, "git push", true, ts(0)),
				inv("s1", "e2", 0, "git push", false, ts(1)),
				// s2 never resolves; same family in s1 must not leak across.
				inv("s2", "e2", 0, "ls", false, ts(1)),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e2",
					ResolutionPath: []string{"git push"},
					OpenedAt:       ts(0),
					ResolvedAt:     ts(1),
				},
				{
					SessionKey:    "s2",
					OpenEntryID:   "e1",
					OpenOrdinal:   0,
					ToolName:      "Bash",
					CommandFamily: "git push",
					Resolved:      false,
					OpenedAt:      ts(0),
				},
			},
		},
		{
			name: "unparseable timestamps do not abandon on gap",
			invs: []ToolInvocation{
				inv("s1", "e1", 0, "git push", true, "not-a-time"),
				inv("s1", "e2", 0, "git push", false, "also-bad"),
			},
			cfg: cfg,
			want: []FailureEpisode{
				{
					SessionKey:     "s1",
					OpenEntryID:    "e1",
					OpenOrdinal:    0,
					ToolName:       "Bash",
					CommandFamily:  "git push",
					Resolved:       true,
					ResolveEntryID: "e2",
					ResolutionPath: []string{"git push"},
					OpenedAt:       "not-a-time",
					ResolvedAt:     "also-bad",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assembleEpisodes(tt.invs, tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("assembleEpisodes mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

// TestEpisodeInvariants is the correctness-by-construction property test:
// over randomly generated invocation slices, every emitted episode must
// satisfy the structural invariants tying Resolved to its companion fields.
func TestEpisodeInvariants(t *testing.T) {
	families := []string{"git push", "npm test", "ls", "echo"}
	sessions := []string{"sa", "sb"}

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 30).Draw(rt, "n")
		invs := make([]ToolInvocation, 0, n)
		for i := 0; i < n; i++ {
			useTS := rapid.Bool().Draw(rt, "useTS")
			tsv := ""
			if useTS {
				tsv = ts(rapid.IntRange(0, 90).Draw(rt, "min"))
			}
			invs = append(invs, ToolInvocation{
				SessionKey:    rapid.SampledFrom(sessions).Draw(rt, "session"),
				EntryID:       rapid.StringMatching(`e[0-9]`).Draw(rt, "entry"),
				Ordinal:       rapid.IntRange(0, 3).Draw(rt, "ord"),
				ToolName:      "Bash",
				CommandFamily: rapid.SampledFrom(families).Draw(rt, "family"),
				IsError:       rapid.Bool().Draw(rt, "isError"),
				Timestamp:     tsv,
			})
		}

		cfg := EpisodeConfig{
			MaxGap:   time.Duration(rapid.IntRange(1, 120).Draw(rt, "gap")) * time.Minute,
			MaxSteps: rapid.IntRange(1, 50).Draw(rt, "steps"),
		}

		eps := assembleEpisodes(invs, cfg)

		for _, e := range eps {
			// Every episode opens on a real failing opener.
			if e.OpenEntryID == "" {
				rt.Fatalf("episode has empty OpenEntryID: %+v", e)
			}
			// Resolved iff ResolveEntryID set.
			if e.Resolved != (e.ResolveEntryID != "") {
				rt.Fatalf("Resolved=%v but ResolveEntryID=%q", e.Resolved, e.ResolveEntryID)
			}
			// Resolved iff ResolutionPath non-nil.
			if e.Resolved != (e.ResolutionPath != nil) {
				rt.Fatalf("Resolved=%v but ResolutionPath=%v", e.Resolved, e.ResolutionPath)
			}
			// ResolvedAt is one-directional: a set ResolvedAt implies Resolved
			// (but a resolved episode may have an empty ResolvedAt if the
			// resolving invocation carried no timestamp).
			if e.ResolvedAt != "" && !e.Resolved {
				rt.Fatalf("ResolvedAt=%q on an abandoned episode", e.ResolvedAt)
			}
			if e.Resolved {
				// A resolved path is never empty and ends at the opener's family.
				if len(e.ResolutionPath) == 0 {
					rt.Fatalf("resolved episode has empty ResolutionPath: %+v", e)
				}
				last := e.ResolutionPath[len(e.ResolutionPath)-1]
				if last != e.CommandFamily {
					rt.Fatalf("ResolutionPath tail %q != CommandFamily %q", last, e.CommandFamily)
				}
				// When both timestamps are present, the resolving success comes
				// at/after the opener (episodes never run backwards in time).
				if e.ResolvedAt != "" && e.OpenedAt != "" && e.ResolvedAt < e.OpenedAt {
					rt.Fatalf("ResolvedAt %q precedes OpenedAt %q", e.ResolvedAt, e.OpenedAt)
				}
			} else if e.ResolvedAt != "" {
				rt.Fatalf("abandoned episode has ResolvedAt=%q", e.ResolvedAt)
			}
		}
	})
}
