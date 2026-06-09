package corpus

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adewale/aha/internal/model"
)

// SparkBuckets is the fixed number of time buckets in an incident sparkline.
const SparkBuckets = 12

// Incident merges a failure cluster's recurrence (how often / how widely it
// fails) with its resolution status (whether, and how, it gets fixed). It is
// the dashboard's primary unit: one row answers "what keeps breaking, and do we
// know how to fix it?". It is computed entirely from failure_episodes, so every
// failing tool call is represented as exactly one episode (consecutive retries
// of the same struggle collapse into one).
type Incident struct {
	ToolName       string           `json:"tool_name"`
	CommandFamily  string           `json:"command_family"`
	ErrorSignature string           `json:"error_signature"`
	Episodes       int              `json:"episodes"`
	Sessions       int              `json:"distinct_sessions"`
	Projects       int              `json:"distinct_projects"`
	Resolved       int              `json:"resolved"`
	ResolutionRate float64          `json:"resolution_rate"`
	State          string           `json:"state"` // "unresolved" | "partial" | "resolved"
	Tier           string           `json:"tier"`  // "" unless resolved>0
	FirstSeen      string           `json:"first_seen"`
	LastSeen       string           `json:"last_seen"`
	Spark          []int            `json:"spark"` // occurrence counts over the global episode window
	Paths          []ResolutionPath `json:"paths"` // top-K resolved paths; empty when unresolved
	SampleRef      string           `json:"sample_ref,omitempty"`
	Score          float64          `json:"score"`
}

// IncidentFilter scopes the incident list. Empty fields impose no constraint;
// State, when set, must be unresolved, partial, or resolved. Limit<=0 uses
// the default page size and is clamped to MaxClusterLimit.
type IncidentFilter struct {
	Project string
	Source  string
	Machine string
	Tool    string
	State   string
	Limit   int
}

// incidentState classifies a cluster by how much of its recurrence is resolved.
func incidentState(resolved, episodes int) string {
	switch {
	case resolved <= 0:
		return "unresolved"
	case resolved >= episodes:
		return "resolved"
	default:
		return "partial"
	}
}

// Incidents returns the unified failure→fix view, ranked by recurrence-and-
// spread so the loudest pain surfaces first regardless of whether it has a fix
// yet (callers filter by State to get e.g. "recurring but unresolved").
func Incidents(db *sql.DB, f IncidentFilter) ([]Incident, error) {
	state, err := normalizeIncidentState(f.State)
	if err != nil {
		return nil, err
	}
	f.State = state

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxClusterLimit {
		limit = MaxClusterLimit
	}

	where, args := incidentWhere(f)
	q := `
select fe.tool_name, fe.command_family, fe.error_signature,
       count(*) as episodes,
       sum(fe.resolved) as resolved,
       count(distinct fe.session_key) as sessions,
       count(distinct fe.project_key) as projects,
       count(distinct case when fe.resolved=1 then fe.session_key end) as rsessions,
       min(fe.opened_at) as first_seen,
       max(fe.opened_at) as last_seen
from failure_episodes fe
` + incidentJoin(f) + where + `
group by fe.tool_name, fe.command_family, fe.error_signature
` + incidentHaving(f)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var incidents []Incident
	for rows.Next() {
		var in Incident
		var rsessions int
		var first, last sql.NullString
		if err := rows.Scan(&in.ToolName, &in.CommandFamily, &in.ErrorSignature,
			&in.Episodes, &in.Resolved, &in.Sessions, &in.Projects, &rsessions, &first, &last); err != nil {
			_ = rows.Close()
			return nil, err
		}
		in.FirstSeen, in.LastSeen = first.String, last.String
		if in.Episodes > 0 {
			in.ResolutionRate = float64(in.Resolved) / float64(in.Episodes)
		}
		in.State = incidentState(in.Resolved, in.Episodes)
		if in.Resolved > 0 {
			in.Tier = candidateTier(in.Resolved, rsessions)
		}
		in.Score = clusterScore(in.Episodes, in.Sessions, in.Projects)
		incidents = append(incidents, in)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(incidents, func(i, j int) bool {
		a, b := incidents[i], incidents[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Episodes != b.Episodes {
			return a.Episodes > b.Episodes
		}
		if a.ToolName != b.ToolName {
			return a.ToolName < b.ToolName
		}
		if a.CommandFamily != b.CommandFamily {
			return a.CommandFamily < b.CommandFamily
		}
		return a.ErrorSignature < b.ErrorSignature
	})
	if len(incidents) > limit {
		incidents = incidents[:limit]
	}

	// Attach resolution paths (resolved incidents) and a sample ref to drill
	// into, only for the surviving page — bounding the follow-up queries to
	// <= limit.
	for i := range incidents {
		if incidents[i].Resolved > 0 {
			paths, err := resolutionPaths(db, f, incidents[i].ToolName, incidents[i].CommandFamily, incidents[i].ErrorSignature)
			if err != nil {
				return nil, err
			}
			incidents[i].Paths = paths
			if len(paths) > 0 {
				incidents[i].SampleRef = paths[0].SampleRef
			}
		}
		if incidents[i].SampleRef == "" {
			ref, err := incidentFailureRef(db, f, incidents[i])
			if err != nil {
				return nil, err
			}
			incidents[i].SampleRef = ref
		}
	}

	spark, err := incidentSparklines(db, f, incidents)
	if err != nil {
		return nil, err
	}
	for i := range incidents {
		incidents[i].Spark = spark[incidentKey(incidents[i].ToolName, incidents[i].CommandFamily, incidents[i].ErrorSignature)]
	}

	if incidents == nil {
		incidents = []Incident{}
	}
	return incidents, nil
}

// incidentJoin adds the sessions join only when a source/machine facet needs it,
// keeping the common (project/tool/no-filter) path index-only.
func incidentJoin(f IncidentFilter) string {
	if f.Source != "" || f.Machine != "" {
		return "join sessions s on s.session_key = fe.session_key\n"
	}
	return ""
}

func normalizeIncidentState(state string) (string, error) {
	switch state {
	case "", "unresolved", "partial", "resolved":
		return state, nil
	default:
		return "", fmt.Errorf("invalid incident state %q (want unresolved, partial, or resolved)", state)
	}
}

func incidentWhere(f IncidentFilter) (string, []any) {
	var conds []string
	var args []any
	if f.Tool != "" {
		conds = append(conds, "fe.tool_name = ?")
		args = append(args, f.Tool)
	}
	if f.Project != "" {
		conds = append(conds, "fe.project_key = ?")
		args = append(args, f.Project)
	}
	if f.Source != "" {
		conds = append(conds, "s.source_name = ?")
		args = append(args, f.Source)
	}
	if f.Machine != "" {
		conds = append(conds, "s.machine_id = ?")
		args = append(args, f.Machine)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "where " + strings.Join(conds, " and ") + "\n", args
}

func incidentHaving(f IncidentFilter) string {
	switch f.State {
	case "unresolved":
		return "having sum(fe.resolved) = 0"
	case "partial":
		return "having sum(fe.resolved) > 0 and sum(fe.resolved) < count(*)"
	case "resolved":
		return "having sum(fe.resolved) = count(*)"
	default:
		return ""
	}
}

// incidentFailureRef returns a ref into a representative failing opener for an
// incident with no resolved sample (e.g. a still-unresolved cluster), so the
// dashboard can always drill into the pain even when there is no fix yet.
func incidentFailureRef(db *sql.DB, f IncidentFilter, in Incident) (string, error) {
	where, args := incidentWhere(f)
	clause := "where"
	if where != "" {
		clause = strings.TrimPrefix(where, "where") // reuse the same facet conds
		clause = "where" + clause + " and"
	}
	q := `select fe.session_key, fe.open_entry_id
from failure_episodes fe
` + incidentJoin(f) + clause + ` fe.tool_name=? and fe.command_family=? and fe.error_signature=?
order by fe.opened_at desc, fe.session_key desc, fe.open_entry_id desc
limit 1`
	args = append(append([]any{}, args...), in.ToolName, in.CommandFamily, in.ErrorSignature)
	var sessionKey, entryID string
	err := db.QueryRow(q, args...).Scan(&sessionKey, &entryID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if ref, err := messageRefText(sessionKey, entryID); err == nil {
		return ref, nil
	}
	return "", nil
}

func incidentKey(tool, family, sig string) string {
	return tool + "\x00" + family + "\x00" + sig
}

// incidentSparklines computes, in one pass, an occurrence histogram per incident
// over a single global time window (so sparklines are comparable across rows).
// Episodes with an unparseable opened_at are dropped from the histogram only.
func incidentSparklines(db *sql.DB, f IncidentFilter, incidents []Incident) (map[string][]int, error) {
	want := map[string]bool{}
	for _, in := range incidents {
		want[incidentKey(in.ToolName, in.CommandFamily, in.ErrorSignature)] = true
	}
	where, args := incidentWhere(f)
	q := `select fe.tool_name, fe.command_family, fe.error_signature, fe.opened_at
from failure_episodes fe
` + incidentJoin(f) + where
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	type ev struct {
		key string
		t   time.Time
	}
	var evs []ev
	var lo, hi time.Time
	have := false
	for rows.Next() {
		var tool, family, sig string
		var openedAt sql.NullString
		if err := rows.Scan(&tool, &family, &sig, &openedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		key := incidentKey(tool, family, sig)
		if !want[key] {
			continue
		}
		t, ok := parseTimestamp(openedAt.String)
		if !ok {
			continue
		}
		evs = append(evs, ev{key: key, t: t})
		if !have || t.Before(lo) {
			lo = t
		}
		if !have || t.After(hi) {
			hi = t
		}
		have = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[string][]int{}
	for k := range want {
		out[k] = make([]int, SparkBuckets)
	}
	if !have {
		return out, nil
	}
	span := hi.Sub(lo)
	for _, e := range evs {
		bucket := 0
		if span > 0 {
			bucket = int(float64(e.t.Sub(lo)) / float64(span) * float64(SparkBuckets-1))
		}
		if bucket < 0 {
			bucket = 0
		}
		if bucket >= SparkBuckets {
			bucket = SparkBuckets - 1
		}
		out[e.key][bucket]++
	}
	return out, nil
}

// TrajectoryStep is one tool call in a failure→fix arc, with a ref so a reader
// can open the exact command.
type TrajectoryStep struct {
	Family    string `json:"family"`
	Ref       string `json:"ref"`
	Ordinal   int    `json:"ordinal"`
	IsError   bool   `json:"is_error"`
	Timestamp string `json:"timestamp"`
}

// IncidentTrajectory reconstructs the full fail→fix arc behind a resolving
// success ref: every tool call from the failing opener through the resolving
// success, in order, each with a ref. The input is the resolving success ref a
// ResolutionPath carries as its SampleRef plus, for multi-call transcript
// entries, the path's SampleOrdinal to identify the exact resolving invocation.
func IncidentTrajectory(db *sql.DB, resolveRef string, resolveOrdinal *int) ([]TrajectoryStep, error) {
	parsed, err := model.ParseRef(resolveRef)
	if err != nil {
		return nil, fmt.Errorf("invalid ref: %w", err)
	}
	msg, ok := parsed.(model.MessageRef)
	if !ok {
		return nil, fmt.Errorf("trajectory requires a message ref (msg:v1:...)")
	}
	sessionKey, resolveEntry := msg.Session.String(), msg.Entry.String()

	type episodeMatch struct {
		openEntry      string
		openOrdinal    int
		resolveOrdinal int
	}
	q := `select open_entry_id, open_ordinal, coalesce(resolve_ordinal, -1) from failure_episodes where session_key=? and resolve_entry_id=?`
	args := []any{sessionKey, resolveEntry}
	if resolveOrdinal != nil {
		q += ` and resolve_ordinal=?`
		args = append(args, *resolveOrdinal)
	}
	q += ` order by open_entry_id, open_ordinal, resolve_ordinal limit 2`
	matchRows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var matches []episodeMatch
	for matchRows.Next() {
		var m episodeMatch
		if err := matchRows.Scan(&m.openEntry, &m.openOrdinal, &m.resolveOrdinal); err != nil {
			_ = matchRows.Close()
			return nil, err
		}
		matches = append(matches, m)
	}
	if err := matchRows.Close(); err != nil {
		return nil, err
	}
	if err := matchRows.Err(); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return []TrajectoryStep{}, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("trajectory requires resolve ordinal for multi-invocation resolving entry %q", resolveEntry)
	}
	match := matches[0]

	rows, err := db.Query(`select ti.entry_id, ti.ordinal, e.line_no, ti.command_family, ti.is_error, ti.timestamp
from tool_invocations ti
join entries e on e.session_key=ti.session_key and e.entry_id=ti.entry_id
where ti.session_key=?`, sessionKey)
	if err != nil {
		return nil, err
	}
	type inv struct {
		entryID, family, ts string
		lineNo, ordinal     int
		isError             bool
	}
	var invs []inv
	for rows.Next() {
		var iv inv
		var isErr int
		var fam, ts sql.NullString
		if err := rows.Scan(&iv.entryID, &iv.ordinal, &iv.lineNo, &fam, &isErr, &ts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		iv.family, iv.ts, iv.isError = fam.String, ts.String, isErr != 0
		invs = append(invs, iv)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(invs, func(i, j int) bool {
		ti, oki := parseTimestamp(invs[i].ts)
		tj, okj := parseTimestamp(invs[j].ts)
		if oki != okj {
			return !oki
		}
		if oki && okj && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if invs[i].lineNo != invs[j].lineNo {
			return invs[i].lineNo < invs[j].lineNo
		}
		if invs[i].entryID != invs[j].entryID {
			return invs[i].entryID < invs[j].entryID
		}
		return invs[i].ordinal < invs[j].ordinal
	})

	openIdx, resolveIdx := -1, -1
	for i, iv := range invs {
		if iv.entryID == match.openEntry && iv.ordinal == match.openOrdinal {
			openIdx = i
		}
		if iv.entryID == resolveEntry && iv.ordinal == match.resolveOrdinal {
			resolveIdx = i
		}
	}
	if openIdx == -1 || resolveIdx == -1 || resolveIdx < openIdx {
		return []TrajectoryStep{}, nil
	}
	steps := make([]TrajectoryStep, 0, resolveIdx-openIdx+1)
	for i := openIdx; i <= resolveIdx; i++ {
		iv := invs[i]
		step := TrajectoryStep{Family: iv.family, Ordinal: iv.ordinal, IsError: iv.isError, Timestamp: iv.ts}
		if r, err := messageRefText(sessionKey, iv.entryID); err == nil {
			step.Ref = r
		}
		steps = append(steps, step)
	}
	return steps, nil
}
