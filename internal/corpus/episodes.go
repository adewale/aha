package corpus

import (
	"sort"
	"time"
)

// FailureEpisode is a single-session arc that opens at a failing tool
// invocation and either resolves (a later same-family success within the
// gap/step budget) or is abandoned. It is a pure projection of a slice of
// ToolInvocation values; see docs/outcome-weighting-spec.md.
//
// Correctness-by-construction: the resolved/abandoned shape is built so the
// following invariants always hold, never patched up after the fact:
//
//	Resolved == (ResolveEntryID != "")
//	Resolved == (ResolveOrdinal >= 0)
//	Resolved == (ResolutionPath != nil)
//	len(ResolutionPath) > 0 && ResolutionPath[last] == CommandFamily, when Resolved.
//
// ResolvedAt mirrors the resolving success's timestamp and is "" when abandoned;
// it is NOT a resolved/abandoned discriminator on its own, because a resolving
// invocation may legitimately carry an empty timestamp. The one-directional
// invariant is: ResolvedAt != "" implies Resolved.
type FailureEpisode struct {
	SessionKey     string
	OpenEntryID    string
	OpenOrdinal    int
	ToolName       string
	CommandFamily  string
	ErrorSignature string
	Resolved       bool
	ResolveEntryID string   // "" when abandoned
	ResolveOrdinal int      // -1 when abandoned
	ResolutionPath []string // intervening families (any family) then the resolving family; nil when abandoned
	ProjectKey     string
	OpenedAt       string // opener Timestamp
	ResolvedAt     string // resolver Timestamp; "" when abandoned
}

// EpisodeConfig bounds how far an open episode will look for its resolving
// success before being abandoned.
type EpisodeConfig struct {
	MaxGap   time.Duration // opener→resolver wall-clock budget; default 30m
	MaxSteps int           // opener→resolver invocation count budget (inclusive); default 40
}

// DefaultEpisodeConfig returns the spec defaults: 30-minute gap, 40-step budget.
func DefaultEpisodeConfig() EpisodeConfig {
	return EpisodeConfig{
		MaxGap:   30 * time.Minute,
		MaxSteps: 40,
	}
}

// openEpisode tracks an in-progress episode while scanning a session.
type openEpisode struct {
	out       *FailureEpisode // the episode being built
	openIndex int             // position of opener within the sorted session slice
	openTime  time.Time       // parsed opener timestamp
	openOK    bool            // whether openTime parsed
}

// assembleEpisodes turns paired tool invocations into single-session failure
// episodes. Episodes never cross sessions. Output is deterministic, ordered by
// (SessionKey, OpenedAt, OpenEntryID, OpenOrdinal).
func assembleEpisodes(invs []ToolInvocation, cfg EpisodeConfig) []FailureEpisode {
	if len(invs) == 0 {
		return nil
	}

	bySession := map[string][]ToolInvocation{}
	order := []string{}
	for _, iv := range invs {
		if _, seen := bySession[iv.SessionKey]; !seen {
			order = append(order, iv.SessionKey)
		}
		bySession[iv.SessionKey] = append(bySession[iv.SessionKey], iv)
	}

	var episodes []FailureEpisode
	for _, session := range order {
		episodes = append(episodes, assembleSession(bySession[session], cfg)...)
	}

	sort.SliceStable(episodes, func(i, j int) bool {
		a, b := episodes[i], episodes[j]
		if a.SessionKey != b.SessionKey {
			return a.SessionKey < b.SessionKey
		}
		if a.OpenedAt != b.OpenedAt {
			return a.OpenedAt < b.OpenedAt
		}
		if a.OpenEntryID != b.OpenEntryID {
			return a.OpenEntryID < b.OpenEntryID
		}
		return a.OpenOrdinal < b.OpenOrdinal
	})

	return episodes
}

// assembleSession processes one session's invocations (already grouped) into
// episodes. The caller is responsible for cross-session ordering.
func assembleSession(session []ToolInvocation, cfg EpisodeConfig) []FailureEpisode {
	sorted := make([]ToolInvocation, len(session))
	copy(sorted, session)
	sortInvocations(sorted)

	// Each episode is heap-allocated so the open-episode pointers stay valid as
	// more episodes are discovered; results are dereferenced at the end.
	var episodes []*FailureEpisode
	// open holds at most one in-progress episode per command family.
	open := map[string]*openEpisode{}

	for i, iv := range sorted {
		fam := iv.CommandFamily

		if iv.IsError {
			// A same-family failure while an episode is already open is part of
			// the ongoing struggle, not a new episode.
			if _, ongoing := open[fam]; ongoing {
				continue
			}
			t, ok := parseTimestamp(iv.Timestamp)
			ep := &FailureEpisode{
				SessionKey:     iv.SessionKey,
				OpenEntryID:    iv.EntryID,
				OpenOrdinal:    iv.Ordinal,
				ToolName:       iv.ToolName,
				CommandFamily:  fam,
				ErrorSignature: iv.ErrorSignature,
				ProjectKey:     iv.ProjectKey,
				OpenedAt:       iv.Timestamp,
				Resolved:       false,
				ResolveOrdinal: -1,
			}
			episodes = append(episodes, ep)
			open[fam] = &openEpisode{
				out:       ep,
				openIndex: i,
				openTime:  t,
				openOK:    ok,
			}
			continue
		}

		// A success: it can resolve an open episode of the same family.
		ep, ongoing := open[fam]
		if !ongoing {
			continue
		}
		if !withinBudget(ep, i, sorted[i].Timestamp, cfg) {
			// Budget already exceeded by this point: the episode is abandoned
			// and stays so. Once exceeded it can never resolve later either,
			// since later successes are only further away. Drop it from open so
			// it remains abandoned.
			delete(open, fam)
			continue
		}
		resolve(ep, sorted, i)
		delete(open, fam)
	}

	out := make([]FailureEpisode, len(episodes))
	for i, ep := range episodes {
		out[i] = *ep
	}
	return out
}

// withinBudget reports whether a candidate resolver at index resolveIdx is
// inside both the gap and step budgets relative to the open episode.
func withinBudget(ep *openEpisode, resolveIdx int, resolveTS string, cfg EpisodeConfig) bool {
	// Step budget: count of invocations from opener to resolver inclusive.
	steps := resolveIdx - ep.openIndex + 1
	if steps > cfg.MaxSteps {
		return false
	}
	// Gap budget: only enforced when both timestamps parse. An unparseable
	// timestamp never abandons on its own (we rely on the step budget).
	if rt, ok := parseTimestamp(resolveTS); ok && ep.openOK {
		if rt.Sub(ep.openTime) > cfg.MaxGap {
			return false
		}
	}
	return true
}

// resolve stamps an open episode as resolved by the success at resolveIdx,
// building the resolution path (intervening families then the resolving
// family) in one shot so the resolved invariants hold by construction.
func resolve(ep *openEpisode, sorted []ToolInvocation, resolveIdx int) {
	path := make([]string, 0, resolveIdx-ep.openIndex)
	for j := ep.openIndex + 1; j < resolveIdx; j++ {
		path = append(path, sorted[j].CommandFamily)
	}
	path = append(path, sorted[resolveIdx].CommandFamily)

	ep.out.Resolved = true
	ep.out.ResolveEntryID = sorted[resolveIdx].EntryID
	ep.out.ResolveOrdinal = sorted[resolveIdx].Ordinal
	ep.out.ResolvedAt = sorted[resolveIdx].Timestamp
	ep.out.ResolutionPath = path
}

// sortInvocations orders a session's invocations by (Timestamp, LineNo,
// EntryID, Ordinal). Empty/unparseable timestamps sort as earliest.
func sortInvocations(invs []ToolInvocation) {
	sort.SliceStable(invs, func(i, j int) bool {
		ti, oki := parseTimestamp(invs[i].Timestamp)
		tj, okj := parseTimestamp(invs[j].Timestamp)
		if oki != okj {
			// Unparseable (earliest) sorts before parseable.
			return !oki
		}
		if oki && okj && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if invs[i].LineNo != invs[j].LineNo {
			return invs[i].LineNo < invs[j].LineNo
		}
		if invs[i].EntryID != invs[j].EntryID {
			return invs[i].EntryID < invs[j].EntryID
		}
		return invs[i].Ordinal < invs[j].Ordinal
	})
}

// parseTimestamp parses an RFC3339 timestamp, reporting ok=false for empty or
// malformed input.
func parseTimestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
