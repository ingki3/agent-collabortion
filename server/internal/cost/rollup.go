package cost

import "github.com/google/uuid"

// Cost aggregation (PRD FR-7.3, openapi getSessionCost · getWorkspaceCost ·
// CostReport · CostBucket).
//
// FR-7.3 names four units — task · agent · session · runtime — and one badge.
// The badge is monotonic: one estimated row makes the whole report estimated,
// because a sum that mixes a measured number with a guess cannot be presented
// as measured. The other direction matters just as much: an all-measured
// report must NOT be badged, or every session in the product wears "추정" and
// the badge stops meaning anything (E9-09).

// UsageRow is one task's stored usage plus the names the buckets show.
type UsageRow struct {
	TaskID, AgentID, RuntimeID, SessionID         uuid.UUID
	TaskName, AgentName, RuntimeName, SessionName string

	CostUSD   float64
	Estimated bool

	InputTokens, OutputTokens, CacheRead int64
}

// Bucket is one row of a by_* array (contract CostBucket).
type Bucket struct {
	ID        uuid.UUID
	Name      string
	CostUSD   float64
	Estimated bool

	InputTokens, OutputTokens, CacheRead int64
	TaskCount                            int
}

// Report is the contract's CostReport.
type Report struct {
	TotalUSD  float64
	Estimated bool

	InputTokens, OutputTokens, CacheRead int64

	ByTask    []Bucket
	ByAgent   []Bucket
	BySession []Bucket
	ByRuntime []Bucket
}

// Rollup folds usage rows into the four buckets.
//
// Production callers: httpapi.GetSessionCost and httpapi.GetWorkspaceCost.
func Rollup(rows []UsageRow) Report {
	r := Report{ByTask: []Bucket{}, ByAgent: []Bucket{}, BySession: []Bucket{}, ByRuntime: []Bucket{}}
	byTask := newGrouper()
	byAgent := newGrouper()
	bySession := newGrouper()
	byRuntime := newGrouper()
	for _, row := range rows {
		// Measured and estimated rows SUM. Dropping the estimated one
		// understates the bill, which is the number the badge exists to
		// qualify — not to hide.
		r.TotalUSD += row.CostUSD
		r.InputTokens += row.InputTokens
		r.OutputTokens += row.OutputTokens
		r.CacheRead += row.CacheRead
		r.Estimated = r.Estimated || row.Estimated

		byTask.add(row.TaskID, row.TaskName, row)
		byAgent.add(row.AgentID, row.AgentName, row)
		bySession.add(row.SessionID, row.SessionName, row)
		byRuntime.add(row.RuntimeID, row.RuntimeName, row)
	}
	r.ByTask = byTask.buckets()
	r.ByAgent = byAgent.buckets()
	r.BySession = bySession.buckets()
	r.ByRuntime = byRuntime.buckets()
	return r
}

// grouper keeps insertion order so the arrays are stable between calls — a
// report whose rows reshuffle on every poll is unreadable in the UI.
type grouper struct {
	order []uuid.UUID
	index map[uuid.UUID]*Bucket
}

func newGrouper() *grouper { return &grouper{index: map[uuid.UUID]*Bucket{}} }

func (g *grouper) add(id uuid.UUID, name string, row UsageRow) {
	b, ok := g.index[id]
	if !ok {
		b = &Bucket{ID: id, Name: name}
		g.index[id] = b
		g.order = append(g.order, id)
	}
	if b.Name == "" {
		b.Name = name
	}
	b.CostUSD += row.CostUSD
	b.InputTokens += row.InputTokens
	b.OutputTokens += row.OutputTokens
	b.CacheRead += row.CacheRead
	b.Estimated = b.Estimated || row.Estimated
	b.TaskCount++
}

func (g *grouper) buckets() []Bucket {
	out := make([]Bucket, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, *g.index[id])
	}
	return out
}
