package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// contextMetric is one attempt's turn-prompt shape (T-CTX0, plan/research/
// CONTEXT_MEMORY.md 0단계): the size of each brief section and each
// turn-prompt block, as buildBundle wrote them. Writing it never fails the
// claim (storeContextMetric's savepoint). Since 맥락 1단계 ③ (harness
// v0.9.14 §6) the heartbeat half IS read back — sessionStartTokens decides
// from samples[0] whether the next turn resumes; a missing row or a failed
// read only means the cap is not applied.
//
// Section keys are closed:
//
//	brief.1 … brief.5 · brief.8             the brief's own sections ([6]·[7]
//	                                        are empty since v0.9.14)
//	prompt.rebind · prompt.resumed · prompt.delta_head · prompt.truncation_note
//	prompt.history                          ① the room's latest messages
//	prompt.mission_messages                 ② the rest of the mission (a
//	                                        header index since v0.9.18)
//	prompt.mission_ledger                   <mission_ledger> (v0.9.18)
//	prompt.room_decisions · prompt.room_summary   ③
//	prompt.room_artifacts · prompt.reused_context (the old brief [6])
//	prompt.mission_progress · prompt.roster_status · prompt.folders
//	prompt.trigger · prompt.respond
//
// and the "/" keys are PARTS of the block before the slash (already counted
// in it): prompt.history/detail, prompt.trigger/detail — the 작업 내용
// each block carries (② has no detail since v0.9.18: it is one line per
// message). For a resumed
// turn's delta the sections measure `prompt` (the delta); counts carry
// delta=1 and prompt_cold_bytes, and session_start_tokens / session_capped
// record the §6 decision.
type contextMetric struct {
	Sections map[string]sectionSize
	Counts   map[string]int
	Brief    sectionSize
	Prompt   sectionSize
}

type sectionSize struct {
	Bytes     int `json:"bytes"`
	TokensEst int `json:"tokens_est"`
}

func sizeOf(s string) sectionSize {
	return sectionSize{Bytes: len(s), TokensEst: llm.EstimateTokens(s)}
}

func newContextMetric() *contextMetric {
	return &contextMetric{Sections: map[string]sectionSize{}, Counts: map[string]int{}}
}

// add records s under key, summing when the key repeats (a block written in
// more than one piece).
func (m *contextMetric) add(key, s string) {
	if s == "" {
		return
	}
	cur := m.Sections[key]
	z := sizeOf(s)
	cur.Bytes += z.Bytes
	cur.TokensEst += z.TokensEst
	m.Sections[key] = cur
}

// wrote records what b gained since start under key: the pattern is
// `n := b.Len(); <write>; m.wrote(key, b, n)`, so the metric measures the
// bytes that were actually written rather than a second rendering of them.
func (m *contextMetric) wrote(key string, b *strings.Builder, start int) {
	m.add(key, b.String()[start:])
}

// topLevel sums the sections without a "/" for one prefix — it must equal
// the whole text (context_metric_test pins that nothing is written outside a
// section).
func (m *contextMetric) topLevel(prefix string) int {
	n := 0
	for k, v := range m.Sections {
		if strings.HasPrefix(k, prefix) && !strings.Contains(k, "/") {
			n += v.Bytes
		}
	}
	return n
}

// keys is the sorted section list (tests and the report).
func (m *contextMetric) keys() []string {
	out := make([]string, 0, len(m.Sections))
	for k := range m.Sections {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// storeContextMetric writes the claim-time half of the attempt's row. The gap
// is measured from the same lane's previous finished attempt: a lane holds
// one runtime_session_ref (harness §6), so that is the previous turn of the
// runtime session this bundle resumes — and for a cold bundle it is still the
// idle time before the lane woke up.
//
// Measurement never costs the claim: the row goes in under its own savepoint
// and an error is swallowed (rolled back to the savepoint) — the bundle is
// already built and the task already dispatched.
func storeContextMetric(ctx context.Context, tx pgx.Tx, t *tasks.Row, b *contracts.TaskBundle, m *contextMetric, now time.Time) {
	if m == nil || b == nil {
		return
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT context_metric`); err != nil {
		return
	}
	if err := insertContextMetric(ctx, tx, t, b, m, now); err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT context_metric`)
		return
	}
	_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT context_metric`)
}

func insertContextMetric(ctx context.Context, tx pgx.Tx, t *tasks.Row, b *contracts.TaskBundle, m *contextMetric, now time.Time) error {
	sections, err := json.Marshal(m.Sections)
	if err != nil {
		return err
	}
	counts, err := json.Marshal(m.Counts)
	if err != nil {
		return err
	}
	var resumeSession *string
	if b.Resume != nil && b.Resume.SessionID != "" {
		s := b.Resume.SessionID
		resumeSession = &s
	}
	var prevEnd *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT max(ta.finished_at) FROM task_attempt ta JOIN task x ON x.id = ta.task_id
		WHERE x.lane_id = $1 AND ta.finished_at IS NOT NULL AND ta.finished_at <= $4
		  AND NOT (ta.task_id = $2 AND ta.attempt = $3)`, t.LaneID, t.ID, t.Attempt, now).Scan(&prevEnd); err != nil {
		return fmt.Errorf("queue: context metric gap: %w", err)
	}
	var gap *float64
	if prevEnd != nil {
		g := now.Sub(*prevEnd).Seconds()
		gap = &g
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO task_context_metric (task_id, attempt, room_id, agent_id, lane_id, runtime_kind,
		  brief_bytes, brief_tokens_est, prompt_bytes, prompt_tokens_est, sections, counts,
		  planned_resume, resume_session_id, gap_seconds, claimed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (task_id, attempt) DO UPDATE SET
		  brief_bytes = EXCLUDED.brief_bytes, brief_tokens_est = EXCLUDED.brief_tokens_est,
		  prompt_bytes = EXCLUDED.prompt_bytes, prompt_tokens_est = EXCLUDED.prompt_tokens_est,
		  sections = EXCLUDED.sections, counts = EXCLUDED.counts, planned_resume = EXCLUDED.planned_resume,
		  resume_session_id = EXCLUDED.resume_session_id, gap_seconds = EXCLUDED.gap_seconds,
		  claimed_at = EXCLUDED.claimed_at`,
		t.ID, t.Attempt, t.SessionID, t.AgentID, t.LaneID, string(b.Profile.RuntimeKind),
		m.Brief.Bytes, m.Brief.TokensEst, m.Prompt.Bytes, m.Prompt.TokensEst, sections, counts,
		b.Resume != nil, resumeSession, gap, now)
	return err
}
