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
