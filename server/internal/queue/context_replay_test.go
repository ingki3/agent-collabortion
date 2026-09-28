package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// TestReplayContextMetrics is a measuring TOOL, not a regression test (T-CTX0
// 기준선, plan/research/context-memory/04-baseline.md): it replays
// buildBundle over a restored copy of a live database, attempt by attempt,
// and writes the turn-prompt shape each one would have had.
//
// Every attempt is replayed inside its own transaction that is ROLLED BACK:
// the messages, decisions, artifacts and attempts written after the attempt's
// dispatch are deleted first, so the bundle sees the room as it was then.
// Nothing is ever committed — and it only runs against the database named by
// CTX0_REPLAY_DB (a restored snapshot, never a live one).
//
//	CTX0_REPLAY_DB=postgres://…/snapm CTX0_REPLAY_ROOM=<room id> \
//	CTX0_REPLAY_OUT=/tmp/replay.json [CTX0_REPLAY_TEXT_TASK=<task id>] \
//	  go test ./internal/queue -run TestReplayContextMetrics -v
//
// CTX0_REPLAY_TEXT_TASK additionally dumps that attempt's brief + prompt text
// (the recall grader's 「지금 방식의 턴 프롬프트」).
func TestReplayContextMetrics(t *testing.T) {
	url, room, out := os.Getenv("CTX0_REPLAY_DB"), os.Getenv("CTX0_REPLAY_ROOM"), os.Getenv("CTX0_REPLAY_OUT")
	if url == "" || room == "" || out == "" {
		t.Skip("CTX0_REPLAY_DB / CTX0_REPLAY_ROOM / CTX0_REPLAY_OUT not set — replay tool only")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	type att struct {
		Task       uuid.UUID
		Attempt    int
		Agent      string
		Dispatched time.Time
		Runtime    uuid.UUID
	}
	rows, err := pool.Query(ctx, `
		SELECT t.id, ta.attempt, a.name, ta.dispatched_at, COALESCE(ta.runtime_id, t.runtime_id)
		FROM task t JOIN agent a ON a.id = t.agent_id JOIN task_attempt ta ON ta.task_id = t.id
		WHERE t.session_id = $1 AND ta.dispatched_at IS NOT NULL AND COALESCE(ta.runtime_id, t.runtime_id) IS NOT NULL
		ORDER BY ta.dispatched_at`, room)
	if err != nil {
		t.Fatal(err)
	}
	var atts []att
	for rows.Next() {
		var a att
		if err := rows.Scan(&a.Task, &a.Attempt, &a.Agent, &a.Dispatched, &a.Runtime); err != nil {
			t.Fatal(err)
		}
		atts = append(atts, a)
	}
	rows.Close()

	type result struct {
		Task       string                 `json:"task"`
		Attempt    int                    `json:"attempt"`
		Agent      string                 `json:"agent"`
		Dispatched time.Time              `json:"dispatched_at"`
		Brief      sectionSize            `json:"brief"`
		Prompt     sectionSize            `json:"prompt"`
		Sections   map[string]sectionSize `json:"sections"`
		Counts     map[string]int         `json:"counts"`
		// BriefSHA / PrefixSHA: the whole brief and its [1]~[5] part — did
		// the system prompt change between two turns of one session?
		BriefSHA  string `json:"brief_sha"`
		PrefixSHA string `json:"prefix_sha"`
		Error     string `json:"error,omitempty"`
	}
	textTask := os.Getenv("CTX0_REPLAY_TEXT_TASK")
	var results []result
	for _, a := range atts {
		r := result{Task: a.Task.String(), Attempt: a.Attempt, Agent: a.Agent, Dispatched: a.Dispatched}
		m, brief, prompt, err := replayOne(ctx, pool, a.Task, a.Attempt, a.Runtime, a.Dispatched)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Brief, r.Prompt, r.Sections, r.Counts = m.Brief, m.Prompt, m.Sections, m.Counts
			r.BriefSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(brief)))
			prefix := brief
			if i := strings.Index(brief, "[6] Context"); i >= 0 {
				prefix = brief[:i]
			} else if i := strings.Index(brief, "[7] Decision Log"); i >= 0 {
				prefix = brief[:i]
			} else if i := strings.Index(brief, "[8] Instruction"); i >= 0 {
				prefix = brief[:i]
			}
			r.PrefixSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(prefix)))
			if textTask == a.Task.String() {
				_ = os.WriteFile(out+".brief.txt", []byte(brief), 0o644)
				_ = os.WriteFile(out+".prompt.txt", []byte(prompt), 0o644)
			}
		}
		results = append(results, r)
	}
	b, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("replayed %d attempts → %s", len(results), out)
}

func replayOne(ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, attempt int, runtimeID uuid.UUID, at time.Time) (*contextMetric, string, string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var room uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT session_id FROM task WHERE id = $1`, taskID).Scan(&room); err != nil {
		return nil, "", "", err
	}
	// The room as it was at dispatch. `after` is strict: the trigger itself
	// was written before its task was dispatched.
	for _, q := range []string{
		`UPDATE message SET parent_id = NULL WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM message WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM decision WHERE session_id = $1 AND created_at > $2`,
		`DELETE FROM artifact WHERE session_id = $1 AND created_at > $2`,
	} {
		if _, err := tx.Exec(ctx, q, room, at); err != nil {
			return nil, "", "", err
		}
	}
	t, err := tasks.Get(ctx, tx, taskID)
	if err != nil {
		return nil, "", "", err
	}
	t.Attempt = attempt
	b, m, err := buildBundle(ctx, tx, t, runtimeID, "replay", at)
	if err != nil {
		return nil, "", "", err
	}
	return m, b.Brief.Text, b.Prompt, nil
}
