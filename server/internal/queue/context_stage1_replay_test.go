package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// TestReplayContextStage1 is a measuring TOOL, not a regression test (T-CTX1,
// 맥락 1단계 성과 측정): it replays one agent's attempts in a restored copy
// of a live database under the stage-1 rules and writes, per attempt, the
// brief hash, the whole (cold) turn prompt and the resumed-turn delta the
// bundle would carry.
//
// Every attempt runs in its own ROLLED-BACK transaction. The room is cut back
// to the attempt's dispatch (as TestReplayContextMetrics does); the lane is
// given a stand-in runtime ref whose anchor is the room's latest message at
// the SAME LANE's previous dispatch (the anchor the stage-1 server would have
// recorded), the runtime is marked as advertising prompt_cold, and the
// metric rows of the lane are removed so the session cap is left to the
// analysis (the cap decides from session sizes the old regime inflated — the
// script models them, plan/research/context-memory/stage1.py).
//
//	CTX1_REPLAY_DB=postgres://…/snap CTX1_REPLAY_ROOM=<room> CTX1_REPLAY_AGENT=Lead \
//	CTX1_REPLAY_OUT=/tmp/ctx1 go test ./internal/queue -run TestReplayContextStage1 -v
//
// Output: $OUT/replay.json and $OUT/<task>-<attempt>.{brief,prompt,cold}.txt.
func TestReplayContextStage1(t *testing.T) {
	url, room, agent, out := os.Getenv("CTX1_REPLAY_DB"), os.Getenv("CTX1_REPLAY_ROOM"), os.Getenv("CTX1_REPLAY_AGENT"), os.Getenv("CTX1_REPLAY_OUT")
	if url == "" || room == "" || agent == "" || out == "" {
		t.Skip("CTX1_REPLAY_DB / _ROOM / _AGENT / _OUT not set — replay tool only")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	type att struct {
		Task       uuid.UUID
		Attempt    int
		Lane       uuid.UUID
		Dispatched time.Time
		Runtime    uuid.UUID
	}
	rows, err := pool.Query(ctx, `
		SELECT t.id, ta.attempt, t.lane_id, ta.dispatched_at, COALESCE(ta.runtime_id, t.runtime_id)
		FROM task t JOIN agent a ON a.id = t.agent_id JOIN task_attempt ta ON ta.task_id = t.id
		WHERE t.session_id = $1 AND a.name = $2 AND ta.dispatched_at IS NOT NULL AND COALESCE(ta.runtime_id, t.runtime_id) IS NOT NULL
		ORDER BY ta.dispatched_at`, room, agent)
	if err != nil {
		t.Fatal(err)
	}
	var atts []att
	for rows.Next() {
		var a att
		if err := rows.Scan(&a.Task, &a.Attempt, &a.Lane, &a.Dispatched, &a.Runtime); err != nil {
			t.Fatal(err)
		}
		atts = append(atts, a)
	}
	rows.Close()

	type result struct {
		Task       string    `json:"task"`
		Attempt    int       `json:"attempt"`
		Lane       string    `json:"lane"`
		Dispatched time.Time `json:"dispatched_at"`
		BriefSHA   string    `json:"brief_sha"`
		BriefBytes int       `json:"brief_bytes"`
		ColdBytes  int       `json:"cold_bytes"`
		ColdTokens int       `json:"cold_tokens_est"`
		// Delta* are zero when the lane had no previous attempt (no anchor).
		DeltaBytes  int            `json:"delta_bytes"`
		DeltaTokens int            `json:"delta_tokens_est"`
		Anchor      string         `json:"anchor,omitempty"`
		Counts      map[string]int `json:"counts"`
		Error       string         `json:"error,omitempty"`
	}
	lastDispatch := map[uuid.UUID]time.Time{}
	var results []result
	for _, a := range atts {
		r := result{Task: a.Task.String(), Attempt: a.Attempt, Lane: a.Lane.String(), Dispatched: a.Dispatched}
		var prev *time.Time
		if p, ok := lastDispatch[a.Lane]; ok {
			prev = &p
		}
		lastDispatch[a.Lane] = a.Dispatched
		b, err := replayStage1(ctx, pool, a.Task, a.Attempt, a.Lane, a.Runtime, a.Dispatched, prev)
		if err != nil {
			r.Error = err.Error()
			results = append(results, r)
			continue
		}
		cold := b.Prompt
		if b.PromptCold != "" {
			cold = b.PromptCold
			r.DeltaBytes, r.DeltaTokens = len(b.Prompt), sizeOf(b.Prompt).TokensEst
			r.Anchor = b.anchor
		}
		r.BriefSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(b.Brief)))
		r.BriefBytes, r.ColdBytes, r.ColdTokens = len(b.Brief), len(cold), sizeOf(cold).TokensEst
		r.Counts = b.counts
		base := filepath.Join(out, fmt.Sprintf("%s-%d", a.Task, a.Attempt))
		_ = os.WriteFile(base+".brief.txt", []byte(b.Brief), 0o644)
		_ = os.WriteFile(base+".cold.txt", []byte(cold), 0o644)
		if b.PromptCold != "" {
			_ = os.WriteFile(base+".prompt.txt", []byte(b.Prompt), 0o644)
		}
		results = append(results, r)
	}
	js, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(filepath.Join(out, "replay.json"), js, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("replayed %d attempts of %s → %s", len(results), agent, out)
}

type stage1Replay struct {
	Brief, Prompt, PromptCold string
	anchor                    string
	counts                    map[string]int
}

func replayStage1(ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, attempt int, laneID, runtimeID uuid.UUID, at time.Time, prev *time.Time) (*stage1Replay, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var room uuid.UUID
	var kind string
	if err := tx.QueryRow(ctx, `SELECT t.session_id, p.runtime_kind FROM task t JOIN agent_profile p ON p.id = t.profile_id WHERE t.id = $1`, taskID).Scan(&room, &kind); err != nil {
		return nil, err
	}
	for _, q := range []string{
		`UPDATE message SET parent_id = NULL WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM message WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM decision WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM artifact WHERE session_id = $1 AND created_at > $2`,
	} {
		if _, err := tx.Exec(ctx, q, room, at); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE runtime SET daemon_features = '{prompt_cold}' WHERE id = $1`, runtimeID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_context_metric WHERE lane_id = $1`, laneID); err != nil {
		return nil, err
	}
	// A retry's previous attempt must not read as resume_rejected: the
	// resume decision is the analysis's, not the old run's.
	if _, err := tx.Exec(ctx, `UPDATE task_attempt SET resumed = NULL WHERE task_id = $1 AND attempt = $2`, taskID, attempt-1); err != nil {
		return nil, err
	}
	if prev == nil {
		if _, err := tx.Exec(ctx, `UPDATE lane SET runtime_session_ref = NULL, context_anchor_message_id = NULL, context_anchor_at = NULL WHERE id = $1`, laneID); err != nil {
			return nil, err
		}
	} else {
		ref := fmt.Sprintf(`{"runtime_kind":%q,"session_id":"replay","cwd":"/replay","created_at":"2026-01-01T00:00:00Z"}`, kind)
		if _, err := tx.Exec(ctx, `
			UPDATE lane SET runtime_session_ref = $2::jsonb,
			       context_anchor_message_id = (SELECT id FROM message WHERE session_id = $3 AND created_at <= $4 ORDER BY created_at DESC, id DESC LIMIT 1),
			       context_anchor_at = (SELECT created_at FROM message WHERE session_id = $3 AND created_at <= $4 ORDER BY created_at DESC, id DESC LIMIT 1)
			WHERE id = $1`, laneID, ref, room, *prev); err != nil {
			return nil, err
		}
	}
	t, err := tasks.Get(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	t.Attempt = attempt
	b, m, err := buildBundle(ctx, tx, t, runtimeID, "replay", at)
	if err != nil {
		return nil, err
	}
	r := &stage1Replay{Brief: b.Brief.Text, Prompt: b.Prompt, PromptCold: b.PromptCold, counts: m.Counts}
	if b.PromptCold != "" {
		_ = tx.QueryRow(ctx, `SELECT context_anchor_message_id::text FROM lane WHERE id = $1`, laneID).Scan(&r.anchor)
	}
	return r, nil
}
