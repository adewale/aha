package corpus

import (
	"database/sql"
	"encoding/json"
	"sort"
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
		if c.Episodes > 0 {
			c.ResolutionRate = float64(c.Resolved) / float64(c.Episodes)
		}
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

	for i := range candidates {
		paths, err := resolutionPaths(db, &candidates[i])
		if err != nil {
			return nil, err
		}
		candidates[i].Paths = paths
	}

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
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if candidates == nil {
		candidates = []SkillCandidate{}
	}
	return candidates, nil
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
func resolutionPaths(db *sql.DB, c *SkillCandidate) ([]ResolutionPath, error) {
	rows, err := db.Query(`
select resolution_path, session_key, project_key, resolve_entry_id, coalesce(resolved_at,'')
from failure_episodes
where resolved=1 and tool_name=? and command_family=? and error_signature=?
order by resolved_at desc, session_key desc, open_entry_id desc`,
		c.ToolName, c.CommandFamily, c.ErrorSignature)
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
		order         int // first-seen rank for deterministic tie-breaks (rows are newest-first)
	}
	byPath := map[string]*agg{}
	var pathOrder []string
	seen := 0
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
				order:         seen,
			}
			byPath[pathJSON] = a
			pathOrder = append(pathOrder, pathJSON)
			seen++
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

	paths := make([]ResolutionPath, 0, len(pathOrder))
	for _, key := range pathOrder {
		a := byPath[key]
		confidence := wilsonLowerBound(a.support, c.Resolved, wilsonZ95)
		p := ResolutionPath{
			Families:   a.families,
			Support:    a.support,
			Sessions:   len(a.sessions),
			Projects:   len(a.projects),
			Confidence: confidence,
		}
		if ref, err := messageRefText(a.sampleSession, a.sampleEntry); err == nil {
			p.SampleRef = ref
		}
		paths = append(paths, p)
	}

	sort.SliceStable(paths, func(i, j int) bool {
		si := pathScore(paths[i].Confidence, paths[i].Sessions, paths[i].Projects)
		sj := pathScore(paths[j].Confidence, paths[j].Sessions, paths[j].Projects)
		if si != sj {
			return si > sj
		}
		if paths[i].Support != paths[j].Support {
			return paths[i].Support > paths[j].Support
		}
		return joinFamilies(paths[i].Families) < joinFamilies(paths[j].Families)
	})
	if len(paths) > defaultPathsPerCandidate {
		paths = paths[:defaultPathsPerCandidate]
	}
	return paths, nil
}

func joinFamilies(fams []string) string {
	out := ""
	for i, f := range fams {
		if i > 0 {
			out += " > "
		}
		out += f
	}
	return out
}
