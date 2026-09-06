// Wiring for the partial-execution simulator (EVAL E8-04·E8-05).
//
// The tag came off with PR #120. Before it, the stand-in re-posted every
// message under a fresh seq — `colab-cli.md` §1 makes a different seq a NEW
// message and puts de-duplication on the resume prompt, so the run reported one
// duplicate per message and the only way to reach 0 would have been
// content-based de-duplication in the server, which the contract forbids.
// #120 taught the stand-in to obey `posted_message_ids`, which is what spike 4c
// measured real runtimes doing (20/20, zero re-posts).
//
// PRODUCTION CALL SITES (nothing below decides anything — every verdict comes
// from the server):
//
//	postMessage      → POST /api/v1/sessions/{id}/messages with the CLI's
//	                   Idempotency-Key UUIDv5(task:<id>:<seq>) and
//	                   X-Colab-Client-Seq (colab-cli.md §1)
//	requeueAfterKill → queue.ExpireStale (the heartbeat sweep, §7) then
//	                   queue.Claim, whose TaskBundle carries the workdir reuse
//	                   flag, posted_message_ids and the `<resumed>` prompt
//	                   (queue.buildBundle → tasks.PlanAttempt)
//	applyEdit        → a real directory, plus the daemon's §6 workdir report so
//	                   the NEXT attempt reads the same path back from the lane
package sim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// idempotencyNamespace is the CLI's fixed UUIDv5 namespace
// (cli/internal/client/uuid.go, colab-cli.md §1 v0.2).
const idempotencyNamespace = "454e4096-cb98-57f5-b314-6c5499b55cc8"

var (
	pool     *pgxpool.Pool
	srv      *httpapi.Server
	ts       *httptest.Server
	fake     *clock.Fake
	root     string // the real directory the virtual workdirs live under
	seedIDs  seed
	current  uuid.UUID // the task postMessage last created, so applyEdit can bind its workdir
	taskLane = map[uuid.UUID]uuid.UUID{}
	taskTok  = map[uuid.UUID]string{}
)

type seed struct {
	user, workspace, runtime, agent, profile, session uuid.UUID
}

var simEpoch = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	p, drop, err := testdb.Provision(context.Background())
	if err != nil {
		// No database: the whole package has nothing to measure. Skipping here
		// rather than per test keeps `go test ./...` green on a laptop, the
		// same way testdb.New skips elsewhere.
		fmt.Fprintln(os.Stderr, "sim: skipping —", err)
		os.Exit(0)
	}
	pool = p
	fake = clock.NewFake(simEpoch)
	srv = httpapi.NewServer(httpapi.Deps{DB: pool, Clock: fake, ServerURL: "http://colab.test"})
	ts = httptest.NewServer(srv.Handler())
	root, err = os.MkdirTemp("", "colab-sim-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim:", err)
		drop()
		os.Exit(1)
	}
	if err := plant(); err != nil {
		fmt.Fprintln(os.Stderr, "sim: seed:", err)
		drop()
		os.Exit(1)
	}
	postMessage = adaptPost
	requeueAfterKill = adaptRequeue
	applyEdit = adaptEdit

	code := m.Run()
	ts.Close()
	_ = os.RemoveAll(root)
	drop()
	os.Exit(code)
}

func plant() error {
	ctx := context.Background()
	q := func(sql string, args ...any) error { _, err := pool.Exec(ctx, sql, args...); return err }
	s := &seedIDs
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ('dir@sim', 'Dir', $1) RETURNING id`, simEpoch).Scan(&s.user); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, created_at, updated_at) VALUES ('sim', 'sim', $1, $1) RETURNING id`, simEpoch).Scan(&s.workspace); err != nil {
		return err
	}
	if err := q(`INSERT INTO workspace_settings (workspace_id) VALUES ($1)`, s.workspace); err != nil {
		return err
	}
	if err := q(`INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`, s.workspace, s.user, simEpoch); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO runtime (workspace_id, name, status, last_seen_at, created_at, updated_at)
		VALUES ($1, 'mac-1', 'online', $2, $2, $2) RETURNING id`, s.workspace, simEpoch).Scan(&s.runtime); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
		VALUES ($1, 'R', 'researcher', 'd', 'i', $2, $3, $3) RETURNING id`, s.workspace, s.user, simEpoch).Scan(&s.agent); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_profile (agent_id, name, runtime_kind, model, is_default, created_at, updated_at)
		VALUES ($1, 'default', 'claude_code', 'claude-sonnet-5', true, $2, $2) RETURNING id`, s.agent, simEpoch).Scan(&s.profile); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO session (workspace_id, title, goal, director_user_id, runtime_id, isolation, status, created_by, created_at, updated_at, started_at)
		VALUES ($1, 'sim', 'g', $2, $3, '{"kind": "none"}'::jsonb, 'active', $2, $4, $4, $4) RETURNING id`,
		s.workspace, s.user, s.runtime, simEpoch).Scan(&s.session); err != nil {
		return err
	}
	return q(`INSERT INTO session_participant (session_id, agent_id, profile_id, joined_at) VALUES ($1, $2, $3, $4)`,
		s.session, s.agent, s.profile, simEpoch)
}

// ensureTask creates the row the simulator's task id names and puts it in the
// state a claim leaves: running on the seed runtime with an attempt token.
//
// Every earlier task is closed first. The simulator runs one at a time, and a
// queue that still holds an older re-queued task would hand THAT one to the
// claim in requeueAfterKill.
func ensureTask(taskID uuid.UUID) error {
	ctx := context.Background()
	if _, ok := taskLane[taskID]; ok {
		return nil
	}
	now := fake.Now()
	if _, err := pool.Exec(ctx, `
		UPDATE task SET status = 'completed', finished_at = $2, updated_at = $2
		WHERE session_id = $1 AND status NOT IN ('completed', 'failed', 'cancelled')`, seedIDs.session, now); err != nil {
		return err
	}
	var msgID, laneID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, created_at)
		VALUES ($1, 'user', $2, '조사해 주세요', $3) RETURNING id`, seedIDs.session, seedIDs.user, now).Scan(&msgID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4) RETURNING id`, seedIDs.session, seedIDs.agent, seedIDs.profile, now).Scan(&laneID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task (id, lane_id, session_id, runtime_id, agent_id, profile_id, trigger_message_id, originator_user_id,
		                  status, attempt, dispatched_at, started_at, heartbeat_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'running', 1, $9, $9, $9, $9, $9)`,
		taskID, laneID, seedIDs.session, seedIDs.runtime, seedIDs.agent, seedIDs.profile, msgID, seedIDs.user, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		UPDATE lane SET status = 'running', updated_at = $2 WHERE id = $1`, laneID, now); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_attempt (task_id, attempt, runtime_id, dispatched_at, started_at) VALUES ($1, 1, $2, $3, $3)`,
		taskID, seedIDs.runtime, now); err != nil {
		return err
	}
	tok, err := srv.Tokens.Issue(ctx, pool, tokens.Scope{
		TaskID: taskID, Attempt: 1, LaneID: laneID, SessionID: seedIDs.session, AgentID: seedIDs.agent, RuntimeID: &seedIDs.runtime,
	})
	if err != nil {
		return err
	}
	taskLane[taskID], taskTok[taskID] = laneID, tok
	return nil
}

// idempotencyKey is the CLI's derivation, reproduced exactly (colab-cli.md §1).
func idempotencyKey(taskID uuid.UUID, seq int) string {
	ns := uuid.MustParse(idempotencyNamespace)
	return uuid.NewSHA1(ns, []byte(fmt.Sprintf("task:%s:%d", taskID, seq))).String()
}

func adaptPost(p postAttempt) postResult {
	if err := ensureTask(p.TaskID); err != nil {
		panic("sim: ensure task: " + err.Error())
	}
	current = p.TaskID
	body, _ := json.Marshal(map[string]any{"content": p.Content})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/sessions/"+seedIDs.session.String()+"/messages", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+taskTok[p.TaskID])
	req.Header.Set("Idempotency-Key", idempotencyKey(p.TaskID, p.Seq))
	req.Header.Set("X-Colab-Client-Seq", fmt.Sprint(p.Seq))
	resp, err := ts.Client().Do(req)
	if err != nil {
		panic("sim: post: " + err.Error())
	}
	defer resp.Body.Close()
	var out struct {
		Message struct {
			ID uuid.UUID `json:"id"`
		} `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	replayed := resp.Header.Get("Idempotent-Replayed") == "true"
	return postResult{
		MessageID: out.Message.ID,
		Created:   resp.StatusCode == http.StatusCreated && !replayed,
		Replayed:  replayed,
		Status:    resp.StatusCode,
	}
}

func adaptRequeue(taskID uuid.UUID) resumeContext {
	ctx := context.Background()
	// The daemon dies: no heartbeat. The server's sweep (daemon-protocol §7) is
	// what notices and re-queues — the simulator does not move the row itself.
	if _, err := pool.Exec(ctx, `UPDATE task SET heartbeat_at = $2 WHERE id = $1`,
		taskID, fake.Now().Add(-2*contracts.HeartbeatExpiry)); err != nil {
		panic("sim: kill: " + err.Error())
	}
	if _, err := srv.Queue.ExpireStale(ctx, fake.Now()); err != nil {
		panic("sim: sweep: " + err.Error())
	}
	fake.Advance(time.Second)
	bundles, err := srv.Queue.Claim(ctx, seedIDs.runtime.String(), 1, fake.Now())
	if err != nil {
		panic("sim: claim: " + err.Error())
	}
	if len(bundles) != 1 || bundles[0].Task.ID != taskID.String() {
		panic(fmt.Sprintf("sim: claim returned %d bundles, want attempt 2 of %s", len(bundles), taskID))
	}
	b := bundles[0]
	taskTok[taskID] = b.TaskToken

	var workdir string
	_ = pool.QueryRow(ctx, `
		SELECT path_or_ref FROM workdir WHERE lane_id = $1 AND status <> 'deleted' ORDER BY created_at DESC LIMIT 1`,
		taskLane[taskID]).Scan(&workdir)

	posted := make([]uuid.UUID, 0, len(b.PostedMessageIDs))
	for _, id := range b.PostedMessageIDs {
		if u, err := uuid.Parse(id); err == nil {
			posted = append(posted, u)
		}
	}
	// /cli/context is where the CLI reads last_seq (colab-cli.md §1 v0.3).
	lastSeq := 0
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/cli/context", nil)
	req.Header.Set("Authorization", "Bearer "+b.TaskToken)
	if resp, err := ts.Client().Do(req); err == nil {
		var out struct {
			LastSeq int `json:"last_seq"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		lastSeq = out.LastSeq
	}
	return resumeContext{
		Attempt: b.Task.Attempt, Workdir: workdir, PostedMessageIDs: posted,
		PromptSaysInspectWorkdir: strings.Contains(b.Prompt, "inspect the current state of the workdir"),
		LastSeq:                  lastSeq,
	}
}

// adaptEdit applies one edit to a REAL directory and reports how many times the
// marker is present afterwards. An empty marker is the inspect-only path: the
// agent looked and did not write.
//
// The first edit of a task also reports the workdir the way a daemon does
// (daemon-protocol §6), so the next attempt reads the same path back off the
// lane instead of the simulator handing it over out of band.
func adaptEdit(workdir string, e editRecord) int {
	dir := realDir(workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic("sim: mkdir: " + err.Error())
	}
	if current != uuid.Nil {
		recordWorkdir(current, workdir)
	}
	path := filepath.Join(dir, filepath.Base(e.Path))
	if e.Marker != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic("sim: edit: " + err.Error())
		}
		_, _ = f.WriteString(e.Marker + "\n")
		_ = f.Close()
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	marker := e.Marker
	if marker == "" {
		// The inspect path passes no marker; the count asked for is of the
		// marker that BELONGS to this edit, which is derived from the path the
		// simulator uses (notes-N.md ↔ <edit-N>).
		marker = markerFor(e.Path)
	}
	return strings.Count(string(body), marker)
}

// markerFor mirrors the simulator's own pairing of notes-N.md with <edit-N>.
func markerFor(path string) string {
	n := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "notes-"), ".md")
	return "<edit-" + n + ">"
}

func realDir(virtual string) string {
	sum := sha256.Sum256([]byte(virtual))
	return filepath.Join(root, hex.EncodeToString(sum[:8]))
}

var workdirRecorded = map[uuid.UUID]bool{}

func recordWorkdir(taskID uuid.UUID, path string) {
	if workdirRecorded[taskID] {
		return
	}
	workdirRecorded[taskID] = true
	ctx := context.Background()
	lane := taskLane[taskID]
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO workdir (session_id, lane_id, kind, path_or_ref, status, last_used_at, created_at, updated_at)
		VALUES ($1, $2, 'dir', $3, 'active', $4, $4, $4)
		ON CONFLICT (session_id, path_or_ref) DO UPDATE SET last_used_at = EXCLUDED.last_used_at
		RETURNING id`, seedIDs.session, lane, path, fake.Now()).Scan(&id); err != nil {
		panic("sim: workdir: " + err.Error())
	}
	if _, err := pool.Exec(ctx, `UPDATE lane SET workdir_id = $2, updated_at = $3 WHERE id = $1`, lane, id, fake.Now()); err != nil {
		panic("sim: lane workdir: " + err.Error())
	}
}
