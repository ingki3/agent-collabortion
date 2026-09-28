package queue

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// T-CTX0 (plan/research/CONTEXT_MEMORY.md 0단계) — the attempt's context
// metric row: the claim writes the turn prompt's shape, heartbeats append the
// running cache numbers, finish fills whether the runtime resumed, how deep
// the session is, the turn's cache_read/cache_write and its tool calls.

type metricRow struct {
	BriefBytes, PromptBytes int
	Sections                map[string]sectionSize
	Counts                  map[string]int
	PlannedResume           bool
	ResumeSessionID         *string
	Gap                     *float64
	Resumed                 *bool
	Depth                   *int
	FinishSession           *string
	CacheRead, CacheWrite   *int64
	ToolCalls               *int
	ToolKinds               map[string]int
	Samples                 [][]float64
}

func readMetric(t *testing.T, q *Postgres, id uuid.UUID, attempt int) metricRow {
	t.Helper()
	var r metricRow
	var sec, cnt, kinds, samples []byte
	if err := q.DB.QueryRow(context.Background(), `
		SELECT brief_bytes, prompt_bytes, sections, counts, planned_resume, resume_session_id, gap_seconds,
		       resumed, session_depth, finish_session_id, cache_read, cache_write, tool_calls, tool_kinds, samples
		FROM task_context_metric WHERE task_id = $1 AND attempt = $2`, id, attempt).Scan(
		&r.BriefBytes, &r.PromptBytes, &sec, &cnt, &r.PlannedResume, &r.ResumeSessionID, &r.Gap,
		&r.Resumed, &r.Depth, &r.FinishSession, &r.CacheRead, &r.CacheWrite, &r.ToolCalls, &kinds, &samples); err != nil {
		t.Fatalf("metric row (%s, %d): %v", id, attempt, err)
	}
	_ = json.Unmarshal(sec, &r.Sections)
	_ = json.Unmarshal(cnt, &r.Counts)
	_ = json.Unmarshal(kinds, &r.ToolKinds)
	_ = json.Unmarshal(samples, &r.Samples)
	return r
}

// toolEvent writes one normalized tool event the way the daemon's events land.
func run(t *testing.T, q *Postgres, id uuid.UUID, attempt int) {
	t.Helper()
	for _, ph := range []string{"preparing", "running"} {
		if err := q.Tasks.Phase(context.Background(), id, attempt, ph); err != nil {
			t.Fatalf("phase %s: %v", ph, err)
		}
	}
}

func toolEvent(t *testing.T, q *Postgres, id uuid.UUID, attempt, seq int, verb, callID, title string) {
	t.Helper()
	p, _ := json.Marshal(map[string]string{"tool_call_id": callID, "title": title})
	if _, err := q.DB.Exec(context.Background(), `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, payload) VALUES ($1, $2, $3, 'tool', $4, $5)`,
		id, attempt, seq, verb, p); err != nil {
		t.Fatal(err)
	}
}

func TestContextMetricFollowsAnAttemptFromClaimToFinish(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	id := testdb.AddTask(t, q.DB, s, s.SessionID, t0)
	if _, err := q.DB.Exec(ctx, `INSERT INTO message (session_id, author_type, content) VALUES ($1, 'system', 'hello room')`, s.SessionID); err != nil {
		t.Fatal(err)
	}

	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 {
		t.Fatalf("claim = %v %v", bundles, err)
	}
	b := bundles[0]
	run(t, q, id, 1)
	m := readMetric(t, q, id, 1)

	// The brief and the turn prompt are measured as sent, and every byte
	// belongs to exactly one top-level section — a block written outside
	// the meter would show up here as a gap.
	if m.BriefBytes != len(b.Brief.Text) || m.PromptBytes != len(b.Prompt) {
		t.Fatalf("totals = brief %d prompt %d, bundle has %d / %d", m.BriefBytes, m.PromptBytes, len(b.Brief.Text), len(b.Prompt))
	}
	cm := &contextMetric{Sections: m.Sections}
	if got := cm.topLevel("brief."); got != len(b.Brief.Text) {
		t.Errorf("brief sections sum to %d, brief is %d bytes (%v)", got, len(b.Brief.Text), cm.keys())
	}
	if got := cm.topLevel("prompt."); got != len(b.Prompt) {
		t.Errorf("prompt sections sum to %d, prompt is %d bytes (%v)", got, len(b.Prompt), cm.keys())
	}
	for _, k := range []string{"brief.1", "brief.2", "brief.4", "brief.5", "brief.8", "prompt.history", "prompt.roster_status", "prompt.trigger", "prompt.respond"} {
		if m.Sections[k].Bytes == 0 || m.Sections[k].TokensEst == 0 {
			t.Errorf("section %s = %+v, want a size", k, m.Sections[k])
		}
	}
	if h := m.Sections["prompt.history"]; !strings.Contains(b.Prompt, "hello room") || h.Bytes < len("hello room") {
		t.Errorf("history section %+v does not cover the room's message", h)
	}
	var total int
	if err := q.DB.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1`, s.SessionID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if m.Counts["history"] != total || m.Counts["history_total"] != total || m.Counts["trigger_messages"] != 1 {
		t.Errorf("counts = %v, want history %d of %d and the seed's one trigger", m.Counts, total, total)
	}
	if m.PlannedResume || m.ResumeSessionID != nil || m.Gap != nil {
		t.Errorf("first attempt: planned_resume=%v session=%v gap=%v, want false/nil/nil", m.PlannedResume, m.ResumeSessionID, m.Gap)
	}

	// Heartbeats: a repeated reading adds no sample.
	for i, u := range []contracts.Usage{
		{InputTokens: 10, CacheReadTokens: 1000, CacheWriteTokens: 500, Estimated: true},
		{InputTokens: 10, CacheReadTokens: 1000, CacheWriteTokens: 500, Estimated: true},
		{InputTokens: 12, CacheReadTokens: 3500, CacheWriteTokens: 700, Estimated: true},
	} {
		c.Advance(15 * time.Second)
		if err := q.Tasks.RecordTurnUsage(ctx, id, 1, u, c.Now()); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
	}
	if got := readMetric(t, q, id, 1).Samples; len(got) != 2 || got[1][1] != 3500 || got[1][2] != 700 || got[0][0] != 15 {
		t.Fatalf("samples = %v, want two: [15 1000 500 10] [45 3500 700 12]", got)
	}

	// Tool calls: one call is several events (start, update, permission) —
	// counted once, by kind; a permission prompt is not a call.
	toolEvent(t, q, id, 1, 1, "run_shell", "c1", "Terminal")
	toolEvent(t, q, id, 1, 2, "permission", "c1", "Terminal")
	toolEvent(t, q, id, 1, 3, "run_shell", "c1", "ls -la")
	toolEvent(t, q, id, 1, 4, "read", "c2", "Read /x")
	toolEvent(t, q, id, 1, 5, "use_tool", "c3", "mcp__colab__colab_message_post")
	toolEvent(t, q, id, 1, 6, "permission", "c2", "Read /x") // the prompt can land after the call's own events

	// attempt 1 dies retryably with a live session → attempt 2 resumes it.
	ref := &contracts.RuntimeSessionRef{RuntimeKind: contracts.RuntimeClaudeCode, SessionID: "acp-sess-1", CWD: "/w", CreatedAt: t0}
	if _, err := q.Tasks.Finish(ctx, id, 1, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther, StopReason: "crash",
		Usage:             contracts.Usage{InputTokens: 20, OutputTokens: 30, CacheReadTokens: 9000, CacheWriteTokens: 800, Estimated: true},
		RuntimeSessionRef: ref}); err != nil {
		t.Fatal(err)
	}
	m = readMetric(t, q, id, 1)
	if m.Resumed != nil || m.Depth == nil || *m.Depth != 0 || m.FinishSession == nil || *m.FinishSession != "acp-sess-1" {
		t.Errorf("attempt 1 finish: resumed=%v depth=%v session=%v, want nil/0/acp-sess-1", m.Resumed, m.Depth, m.FinishSession)
	}
	if m.CacheRead == nil || *m.CacheRead != 9000 || m.CacheWrite == nil || *m.CacheWrite != 800 {
		t.Errorf("attempt 1 cache = %v/%v, want 9000/800 (cache_write has no other column)", m.CacheRead, m.CacheWrite)
	}
	if m.ToolCalls == nil || *m.ToolCalls != 3 || m.ToolKinds["run_shell"] != 1 || m.ToolKinds["read"] != 1 || m.ToolKinds["mcp__colab__colab_message_post"] != 1 {
		t.Errorf("tools = %v %v, want 3: run_shell 1, read 1, mcp__colab__colab_message_post 1", m.ToolCalls, m.ToolKinds)
	}

	c.Advance(90 * time.Second)
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 || bundles[0].Task.Attempt != 2 {
		t.Fatalf("second claim = %v %v", bundles, err)
	}
	run(t, q, id, 2)
	m2 := readMetric(t, q, id, 2)
	if !m2.PlannedResume || m2.ResumeSessionID == nil || *m2.ResumeSessionID != "acp-sess-1" {
		t.Errorf("attempt 2: planned_resume=%v session=%v, want true/acp-sess-1", m2.PlannedResume, m2.ResumeSessionID)
	}
	if m2.Gap == nil || math.Abs(*m2.Gap-90) > 0.01 {
		t.Errorf("attempt 2 gap = %v, want 90s since attempt 1 ended", m2.Gap)
	}
	// A resumed finish that reports no ref still lands on the session it
	// resumed, one turn deep; an empty usage falls back to the last sample.
	c.Advance(15 * time.Second)
	if err := q.Tasks.RecordTurnUsage(ctx, id, 2, contracts.Usage{InputTokens: 3, CacheReadTokens: 12000, CacheWriteTokens: 40, Estimated: true}, c.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Tasks.Finish(ctx, id, 2, contracts.Finish{Outcome: "completed", StopReason: "end_turn", ResumeOutcome: "resumed"}); err != nil {
		t.Fatal(err)
	}
	m2 = readMetric(t, q, id, 2)
	if m2.Resumed == nil || !*m2.Resumed || m2.Depth == nil || *m2.Depth != 1 || m2.FinishSession == nil || *m2.FinishSession != "acp-sess-1" {
		t.Errorf("attempt 2 finish: resumed=%v depth=%v session=%v, want true/1/acp-sess-1", m2.Resumed, m2.Depth, m2.FinishSession)
	}
	if m2.CacheRead == nil || *m2.CacheRead != 12000 || m2.CacheWrite == nil || *m2.CacheWrite != 40 {
		t.Errorf("attempt 2 cache = %v/%v, want the last heartbeat's 12000/40", m2.CacheRead, m2.CacheWrite)
	}
	if m2.ToolCalls == nil || *m2.ToolCalls != 0 {
		t.Errorf("attempt 2 tool calls = %v, want 0 — attempt 1's events are not this attempt's", m2.ToolCalls)
	}
}

// Measurement never costs the work it measures: with the metric table gone,
// claim, heartbeat and finish all still succeed.
func TestContextMetricFailureDoesNotFailTheTurn(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	id := testdb.AddTask(t, q.DB, s, s.SessionID, t0)
	if _, err := q.DB.Exec(ctx, `ALTER TABLE task_context_metric RENAME TO task_context_metric_gone`); err != nil {
		t.Fatal(err)
	}
	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 {
		t.Fatalf("claim with the metric table gone = %v %v, want the bundle", bundles, err)
	}
	run(t, q, id, 1)
	c.Advance(15 * time.Second)
	if err := q.Tasks.RecordTurnUsage(ctx, id, 1, contracts.Usage{InputTokens: 1, CacheReadTokens: 2, Estimated: true}, c.Now()); err != nil {
		t.Fatalf("heartbeat with the metric table gone: %v", err)
	}
	final, err := q.Tasks.Finish(ctx, id, 1, contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{InputTokens: 1, CostUSD: 0.1}})
	if err != nil || final != tasks.Completed {
		t.Fatalf("finish with the metric table gone = %s %v, want completed", final, err)
	}
	var n int
	if err := q.DB.QueryRow(ctx, `SELECT count(*) FROM task_usage WHERE task_id = $1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("task_usage rows = %d %v, want the finish's usage stored", n, err)
	}
}

// A long mission room: ① holds the latest 50, ② the rest of the mission,
// ③ the decisions past [7]'s 20 — each bundle is measured on its own, the
// 작업 내용 parts inside them are measured as parts, and the top-level
// sections still add up to the prompt exactly.
func TestContextMetricMeasuresTheThreeHistoryBundles(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	id := testdb.AddTask(t, q.DB, s, s.SessionID, t0)
	for i := 0; i < 70; i++ {
		var detail *string
		if i%10 == 0 {
			d := strings.Repeat("measured detail ", 60)
			detail = &d
		}
		if _, err := q.DB.Exec(ctx, `
			INSERT INTO message (session_id, author_type, author_id, content, detail, created_at, work_id)
			VALUES ($1, 'agent', $2, $3, $4, $5, (SELECT legacy_work_id FROM room WHERE id = $1))`,
			s.SessionID, s.AgentID, "mission line", detail, t0.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		if _, err := q.DB.Exec(ctx, `INSERT INTO decision (session_id, summary, source, created_at) VALUES ($1, 'use plan B', 'agent', $2)`,
			s.SessionID, t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(time.Hour)
	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 {
		t.Fatalf("claim = %v %v", bundles, err)
	}
	b := bundles[0]
	m := readMetric(t, q, id, 1)
	cm := &contextMetric{Sections: m.Sections}
	if got := cm.topLevel("prompt."); got != len(b.Prompt) {
		t.Fatalf("prompt sections sum to %d, prompt is %d bytes (%v)", got, len(b.Prompt), cm.keys())
	}
	if got := cm.topLevel("brief."); got != len(b.Brief.Text) {
		t.Fatalf("brief sections sum to %d, brief is %d bytes (%v)", got, len(b.Brief.Text), cm.keys())
	}
	var total int
	if err := q.DB.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1`, s.SessionID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if m.Counts["history"] != 50 || m.Counts["history_total"] != total || m.Counts["mission_messages"] != total-50 || m.Counts["room_decisions"] != 5 {
		t.Fatalf("counts = %v, want history 50 of %d, mission %d, decisions 5", m.Counts, total, total-50)
	}
	for _, k := range []string{"prompt.truncation_note", "prompt.mission_messages", "prompt.room_decisions", "brief.7",
		"prompt.history/detail", "prompt.mission_messages/detail"} {
		if m.Sections[k].Bytes == 0 {
			t.Errorf("section %s not measured (%v)", k, cm.keys())
		}
	}
	for _, k := range []string{"history", "mission_messages"} {
		part, whole := m.Sections["prompt."+k+"/detail"].Bytes, m.Sections["prompt."+k].Bytes
		if part >= whole {
			t.Errorf("%s detail %d bytes is not a part of the block's %d", k, part, whole)
		}
	}
	if !strings.Contains(b.Prompt, "<mission_messages") || !strings.Contains(b.Prompt, "<room_decisions count=5") {
		t.Fatalf("fixture did not render ② and ③")
	}
}
