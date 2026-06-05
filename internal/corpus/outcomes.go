package corpus

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

// wilsonZ95 is the z-score for a ~95% Wilson interval.
const wilsonZ95 = 1.96

// defaultPathsPerCandidate is the K in the spec's "top-K distinct resolution
// paths" — enough to expose a confounded cluster's competing fixes without
// turning the surface into a dump.
const defaultPathsPerCandidate = 3

// ResolutionPath is one distinct way a cluster's failures got resolved: the
// ordered command families taken from the failure to the fix (fix family last).
type ResolutionPath struct {
	Families   []string `json:"families"`
	Support    int      `json:"support"`
	Sessions   int      `json:"distinct_sessions"`
	Projects   int      `json:"distinct_projects"`
	Confidence float64  `json:"confidence"`
	SampleRef  string   `json:"sample_ref,omitempty"`
}

// SkillCandidate is a failure cluster that has at least one resolved episode,
// carrying its best-ranked resolution paths. It is a strict superset of the
// identity fields of Cluster, so the two surfaces compose.
type SkillCandidate struct {
	ToolName       string           `json:"tool_name"`
	CommandFamily  string           `json:"command_family"`
	ErrorSignature string           `json:"error_signature"`
	Episodes       int              `json:"episodes"`
	Resolved       int              `json:"resolved"`
	ResolutionRate float64          `json:"resolution_rate"`
	Paths          []ResolutionPath `json:"paths"`
	Score          float64          `json:"score"`
	Tier           string           `json:"tier"`
}

// SkillCandidates ranks resolved failure clusters with their best fixes. Only
// clusters with >=1 resolved episode are returned (an unresolved cluster is a
// pain point, surfaced by Clusters, not a skill candidate). limit<=0 uses the
// default page size; positive limits clamp to MaxClusterLimit.
func SkillCandidates(db *sql.DB, limit int) ([]SkillCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxClusterLimit {
		limit = MaxClusterLimit
	}
	rows, err := db.Query(`
select tool_name, command_family, error_signature,
       count(*) as episodes,
       sum(resolved) as resolved,
       count(distinct case when resolved=1 then session_key end) as rsessions,
       count(distinct case when resolved=1 then project_key end) as rprojects
from failure_episodes
group by tool_name, command_family, error_signature
having sum(resolved) >= 1`)
	if err != nil {
		return nil, err
	}
	var candidates []SkillCandidate
	for rows.Next() {
		var c SkillCandidate
		var rsessions, rprojects int
		if err := rows.Scan(&c.ToolName, &c.CommandFamily, &c.ErrorSignature,
			&c.Episodes, &c.Resolved, &rsessions, &rprojects); err != nil {
			_ = rows.Close()
			return nil, err
		}
		// The query's `having sum(resolved) >= 1` guarantees Resolved >= 1, and
		// Episodes = count(*) >= Resolved, so Episodes is always >= 1 here — no
		// divide-by-zero guard needed.
		c.ResolutionRate = float64(c.Resolved) / float64(c.Episodes)
		c.Score = clusterScore(c.Resolved, rsessions, rprojects)
		c.Tier = candidateTier(c.Resolved, rsessions)
		candidates = append(candidates, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Rank and truncate BEFORE fetching resolution paths: the per-candidate
	// Score depends only on the aggregate columns above, so this reordering is
	// behavior-preserving and bounds the follow-up path queries to <= limit
	// (rather than one per resolved cluster in the whole corpus).
	sortCandidates(candidates)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	for i := range candidates {
		paths, err := resolutionPaths(db, candidates[i].ToolName, candidates[i].CommandFamily, candidates[i].ErrorSignature)
		if err != nil {
			return nil, err
		}
		candidates[i].Paths = paths
	}

	if candidates == nil {
		candidates = []SkillCandidate{}
	}
	return candidates, nil
}

// sortCandidates orders candidates by recurrence-and-spread score, then by
// resolved count, then lexically — a total order so output is deterministic.
func sortCandidates(candidates []SkillCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Resolved != b.Resolved {
			return a.Resolved > b.Resolved
		}
		if a.ToolName != b.ToolName {
			return a.ToolName < b.ToolName
		}
		if a.CommandFamily != b.CommandFamily {
			return a.CommandFamily < b.CommandFamily
		}
		return a.ErrorSignature < b.ErrorSignature
	})
}

// candidateTier marks how much evidence backs a candidate: "established" needs
// recurrence and spread (resolved >=3 across >=2 sessions); everything else is
// "tentative" so thin, single-occurrence evidence is never dressed up as a
// confident fix.
func candidateTier(resolved, sessions int) string {
	if resolved >= 3 && sessions >= 2 {
		return "established"
	}
	return "tentative"
}

// resolutionPaths groups a cluster's resolved episodes by their normalized
// command-family sequence, ranks them by a small-N-aware score, and returns the
// top K. Confidence is the Wilson lower bound of "of the times this failure was
// fixed, how often was it fixed THIS way" — so a 1-of-1 path ranks below a
// 3-of-4 path even though both are "always" by raw rate.
func resolutionPaths(db *sql.DB, toolName, commandFamily, errorSignature string) ([]ResolutionPath, error) {
	rows, err := db.Query(`
select resolution_path, session_key, project_key, resolve_entry_id, coalesce(resolved_at,'')
from failure_episodes
where resolved=1 and tool_name=? and command_family=? and error_signature=?
order by resolved_at desc, session_key desc, open_entry_id desc`,
		toolName, commandFamily, errorSignature)
	if err != nil {
		return nil, err
	}

	type agg struct {
		families      []string
		support       int
		sessions      map[string]struct{}
		projects      map[string]struct{}
		sampleSession string
		sampleEntry   string
	}
	byPath := map[string]*agg{}
	var pathOrder []string
	for rows.Next() {
		var pathJSON, sessionKey, projectKey, resolveEntryID, resolvedAt string
		if err := rows.Scan(&pathJSON, &sessionKey, &projectKey, &resolveEntryID, &resolvedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		a, ok := byPath[pathJSON]
		if !ok {
			var fams []string
			if err := json.Unmarshal([]byte(pathJSON), &fams); err != nil {
				_ = rows.Close()
				return nil, err
			}
			a = &agg{
				families:      fams,
				sessions:      map[string]struct{}{},
				projects:      map[string]struct{}{},
				sampleSession: sessionKey, // rows are newest-first, so the first row is the most recent
				sampleEntry:   resolveEntryID,
			}
			byPath[pathJSON] = a
			pathOrder = append(pathOrder, pathJSON)
		}
		a.support++
		a.sessions[sessionKey] = struct{}{}
		a.projects[projectKey] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// trials = total resolved episodes in this cluster, derived from the SAME
	// rows we just grouped, so support <= trials holds by construction (no
	// dependence on the separately-queried c.Resolved aggregate).
	trials := 0
	for _, a := range byPath {
		trials += a.support
	}

	// Decorate each path with its sort key once (Wilson confidence x spread,
	// then support, then a stable family key), so the comparator below doesn't
	// recompute pathScore or re-join families on every comparison.
	type scoredPath struct {
		path  ResolutionPath
		score float64
		key   string
	}
	decorated := make([]scoredPath, 0, len(pathOrder))
	for _, key := range pathOrder {
		a := byPath[key]
		p := ResolutionPath{
			Families:   a.families,
			Support:    a.support,
			Sessions:   len(a.sessions),
			Projects:   len(a.projects),
			Confidence: wilsonLowerBound(a.support, trials, wilsonZ95),
		}
		// Best-effort: a malformed stored coordinate degrades to an empty
		// SampleRef. The schema CHECK guarantees resolve_entry_id is non-null on
		// resolved rows, so this is defensive, not an expected path.
		if ref, err := messageRefText(a.sampleSession, a.sampleEntry); err == nil {
			p.SampleRef = ref
		}
		decorated = append(decorated, scoredPath{
			path:  p,
			score: pathScore(p.Confidence, p.Sessions, p.Projects),
			key:   strings.Join(a.families, "\x00"),
		})
	}

	sort.SliceStable(decorated, func(i, j int) bool {
		if decorated[i].score != decorated[j].score {
			return decorated[i].score > decorated[j].score
		}
		if decorated[i].path.Support != decorated[j].path.Support {
			return decorated[i].path.Support > decorated[j].path.Support
		}
		return decorated[i].key < decorated[j].key
	})

	n := len(decorated)
	if n > defaultPathsPerCandidate {
		n = defaultPathsPerCandidate
	}
	paths := make([]ResolutionPath, 0, n)
	for i := 0; i < n; i++ {
		paths = append(paths, decorated[i].path)
	}
	return paths, nil
}
