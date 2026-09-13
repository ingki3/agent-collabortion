// Package testchat is FR-1.8.1's test chat (daemon-protocol v0.8 §4.5,
// openapi createTestChat · getTestChat · postTestChatTurn · closeTestChat).
//
// A test chat is NOT a session: no session row, no lane, no task, no task
// token. One user turn is, to the daemon, a token-less attempt of a task whose
// id is the chat's id and whose attempt number is the turn number — so the
// same claim · phase · events · heartbeat · finish endpoints carry it, and the
// server routes them here only when the id is not a task (httpapi.daemon.go,
// "task 조회 실패 뒤에만").
//
// State lives on the test_chat row (migration 0020): turn_status is the
// attempt state of the turn in flight, turns jsonb is the transcript. Nothing
// is written to task_event (§4.5 "저장하지 않고"); message.say is folded into
// the agent turn, usage into the chat's totals, the preview into an ephemeral
// SSE frame.
package testchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

var (
	ErrNotFound = errors.New("testchat: not found")
	// ErrStaleAttempt — the daemon named a turn number that is not the one in
	// flight (a late report of an earlier turn, or a number never handed out).
	ErrStaleAttempt = errors.New("testchat: attempt is not the current turn")
	// ErrInvalidTransition — a phase report the turn's state does not accept
	// (running before preparing is fine; anything after idle is not).
	ErrInvalidTransition = errors.New("testchat: invalid turn transition")
)

// Turn statuses (test_chat.turn_status). `idle` is "no turn in flight".
const (
	TurnIdle       = "idle"
	TurnQueued     = "queued"
	TurnDispatched = "dispatched"
	TurnPreparing  = "preparing"
	TurnRunning    = "running"
)

// Turn is one element of test_chat.turns (openapi TestChatTurn).
type Turn struct {
	Role    string     `json:"role"` // user | agent
	Content string     `json:"content"`
	At      time.Time  `json:"at"`
	Usage   *TurnUsage `json:"usage,omitempty"`
	Error   *string    `json:"error,omitempty"`
}

type TurnUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Row is one test_chat row.
type Row struct {
	ID                uuid.UUID
	WorkspaceID       uuid.UUID
	AgentID           uuid.UUID
	ProfileID         uuid.UUID
	UserID            uuid.UUID
	RuntimeID         *uuid.UUID
	Status            string // open | closed
	Transport         *string
	RuntimeSessionRef []byte
	Turns             []Turn
	InputTokens       int64
	OutputTokens      int64
	CostUSD           float64
	Estimated         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          *time.Time

	TurnStatus   string
	TurnNo       int
	DispatchedAt *time.Time
	StartedAt    *time.Time
	HeartbeatAt  *time.Time
	TurnText     string
	TurnUsage    *contracts.Usage
	TurnLastSeq  int
	WorkdirPath  *string
}

const rowColumns = `id, workspace_id, agent_id, profile_id, user_id, runtime_id, status::text, transport,
	runtime_session_ref, turns, input_tokens, output_tokens, cost_usd, estimated, created_at, updated_at, closed_at,
	turn_status, turn_no, dispatched_at, started_at, heartbeat_at, turn_text, turn_usage, turn_last_seq, workdir_path`

func scan(row pgx.Row) (*Row, error) {
	var r Row
	var turns, usage []byte
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.AgentID, &r.ProfileID, &r.UserID, &r.RuntimeID, &r.Status, &r.Transport,
		&r.RuntimeSessionRef, &turns, &r.InputTokens, &r.OutputTokens, &r.CostUSD, &r.Estimated, &r.CreatedAt, &r.UpdatedAt, &r.ClosedAt,
		&r.TurnStatus, &r.TurnNo, &r.DispatchedAt, &r.StartedAt, &r.HeartbeatAt, &r.TurnText, &usage, &r.TurnLastSeq, &r.WorkdirPath); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("testchat: scan: %w", err)
	}
	if len(turns) > 0 {
		if err := json.Unmarshal(turns, &r.Turns); err != nil {
			return nil, fmt.Errorf("testchat: turns: %w", err)
		}
	}
	if r.Turns == nil {
		r.Turns = []Turn{}
	}
	if len(usage) > 0 {
		var u contracts.Usage
		if err := json.Unmarshal(usage, &u); err == nil {
			r.TurnUsage = &u
		}
	}
	return &r, nil
}

// Get reads one chat.
func Get(ctx context.Context, q db.DBTX, id uuid.UUID) (*Row, error) {
	return scan(q.QueryRow(ctx, `SELECT `+rowColumns+` FROM test_chat WHERE id = $1`, id))
}

// lock reads one chat FOR UPDATE.
func lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Row, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+rowColumns+` FROM test_chat WHERE id = $1 FOR UPDATE`, id))
}

// InFlight reports whether a turn is out with the daemon (any status but idle).
func (r *Row) InFlight() bool { return r.TurnStatus != TurnIdle }

// Service is the test chat's state machine.
type Service struct {
	DB    *pgxpool.Pool
	Clock clock.Clock
	Hub   *realtime.Hub
	Log   *slog.Logger
	// Notify wakes the claim long-poll after a turn is queued
	// (queue.Notifier.Wake). A hook because internal/queue imports this
	// package to hand the turn out.
	Notify func()
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Log: slog.Default()}
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// openapi surface
// ---------------------------------------------------------------------------

// CreateInput is createTestChat's body plus the caller.
type CreateInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	UserID      uuid.UUID
	ProfileID   *uuid.UUID // nil → the agent's default profile
	RuntimeID   *uuid.UUID // nil → any online runtime of the profile's runtime_kind
}

// Create opens a chat (openapi createTestChat). The runtime is fixed here
// (§4.5 "test_chat.runtime_id 는 생성 시 고정된다"): the one the caller named,
// which must be online and of this workspace, or any online runtime that
// advertises the profile's runtime_kind. Neither → 409 in the user's words.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Row, error) {
	now := s.Clock.Now()
	var out *Row
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var profileID uuid.UUID
		var runtimeKind string
		var archived *time.Time
		if in.ProfileID != nil {
			err := tx.QueryRow(ctx, `
				SELECT p.id, p.runtime_kind::text, a.archived_at FROM agent_profile p JOIN agent a ON a.id = p.agent_id
				WHERE p.id = $1 AND p.agent_id = $2`, *in.ProfileID, in.AgentID).Scan(&profileID, &runtimeKind, &archived)
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("profile")
			}
			if err != nil {
				return err
			}
		} else {
			err := tx.QueryRow(ctx, `
				SELECT p.id, p.runtime_kind::text, a.archived_at FROM agent_profile p JOIN agent a ON a.id = p.agent_id
				WHERE p.agent_id = $1 AND p.is_default`, in.AgentID).Scan(&profileID, &runtimeKind, &archived)
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Conflict("no_default_profile", "이 에이전트에 기본 프로파일이 없습니다 — 프로파일 하나를 기본으로 지정한 뒤 다시 시도해 주세요")
			}
			if err != nil {
				return err
			}
		}
		if archived != nil {
			return apperr.Conflict("agent_archived", "보관된 에이전트와는 시험 대화를 할 수 없습니다")
		}
		runtimeID, err := pickRuntime(ctx, tx, in.WorkspaceID, runtimeKind, in.RuntimeID)
		if err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO test_chat (workspace_id, agent_id, profile_id, user_id, runtime_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'open', $6, $6) RETURNING id`,
			in.WorkspaceID, in.AgentID, profileID, in.UserID, runtimeID, now).Scan(&id); err != nil {
			return fmt.Errorf("testchat: insert: %w", err)
		}
		out, err = Get(ctx, tx, id)
		return err
	})
	return out, err
}

// pickRuntime fixes the chat's machine (§4.5, openapi createTestChat 409).
func pickRuntime(ctx context.Context, tx pgx.Tx, wsID uuid.UUID, runtimeKind string, want *uuid.UUID) (uuid.UUID, error) {
	if want != nil {
		var status string
		err := tx.QueryRow(ctx, `SELECT status::text FROM runtime WHERE id = $1 AND workspace_id = $2`, *want, wsID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, apperr.NotFound("runtime")
		}
		if err != nil {
			return uuid.Nil, err
		}
		if status != "online" {
			return uuid.Nil, apperr.Conflict("runtime_offline", "선택한 컴퓨터가 연결돼 있지 않습니다 — 컴퓨터를 켜고 다시 연결하거나 다른 컴퓨터를 골라 주세요")
		}
		return *want, nil
	}
	// capabilities is the probe's []Capability; `kind` is the runtime_kind
	// each CLI on the machine advertises (harness §9).
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM runtime
		WHERE workspace_id = $1 AND status = 'online'
		  AND capabilities @> jsonb_build_array(jsonb_build_object('kind', $2::text))
		ORDER BY last_seen_at DESC NULLS LAST, created_at LIMIT 1`, wsID, runtimeKind).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.Conflict("no_online_runtime",
			"이 프로파일을 실행할 수 있는 연결된 컴퓨터가 없습니다 — 컴퓨터를 연결하거나 다른 프로파일로 시험해 주세요")
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// PostTurn appends a user turn and queues it for the chat's runtime (openapi
// postTestChatTurn: 409 while the previous turn is in flight, 410 once closed).
func (s *Service) PostTurn(ctx context.Context, id uuid.UUID, content string) (*Turn, error) {
	now := s.Clock.Now()
	var turn Turn
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Status == "closed" {
			return apperr.Gone("test_chat_closed", "이미 끝난 시험 대화입니다 — 새 시험 대화를 시작해 주세요")
		}
		if r.InFlight() {
			return apperr.Conflict("turn_in_progress", "에이전트가 아직 답하는 중입니다 — 답이 오면 다음 메시지를 보낼 수 있습니다")
		}
		turn = Turn{Role: "user", Content: content, At: now}
		turns := append(r.Turns, turn)
		if _, err := tx.Exec(ctx, `
			UPDATE test_chat SET turns = $2, turn_status = 'queued', turn_no = turn_no + 1,
			       dispatched_at = NULL, started_at = NULL, heartbeat_at = NULL,
			       turn_text = '', turn_usage = NULL, turn_last_seq = 0, updated_at = $3
			WHERE id = $1`, id, turns, now); err != nil {
			return fmt.Errorf("testchat: queue turn: %w", err)
		}
		return nil
	})
	if err == nil && s.Notify != nil {
		s.Notify()
	}
	return &turn, err
}

// Close ends the chat (openapi closeTestChat, idempotent). A turn in flight
// gets a `cancel` (reason director); the runtime always gets a `gc` naming the
// temporary directory (§4.5 "닫기·취소") — with test_chat_id and no session_id.
func (s *Service) Close(ctx context.Context, id uuid.UUID) (*Row, error) {
	now := s.Clock.Now()
	var out *Row
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Status == "closed" {
			out = r
			return nil
		}
		if r.RuntimeID != nil {
			if r.InFlight() && r.TurnStatus != TurnQueued {
				if err := tokens.QueueCommand(ctx, tx, *r.RuntimeID, contracts.Command{
					Type: contracts.CmdCancel, TaskID: r.ID.String(), Attempt: r.TurnNo, Reason: "director",
				}); err != nil {
					return err
				}
			}
			// The directory exists only once a turn has been dispatched (the
			// daemon mkdir -p's on the first bundle); a chat closed before any
			// turn went out has nothing on disk to delete.
			if r.WorkdirPath != nil && *r.WorkdirPath != "" {
				if err := tokens.QueueCommand(ctx, tx, *r.RuntimeID, contracts.Command{
					Type: contracts.CmdGC, TestChatID: r.ID.String(),
					Workdirs: []contracts.GCWorkdir{{ID: r.ID.String(), Path: *r.WorkdirPath}},
				}); err != nil {
					return err
				}
			}
		}
		// A turn still `queued` was never handed out: it simply stops being a
		// claim candidate. Its user turn stays in the transcript, closed.
		turns := r.Turns
		if r.TurnStatus == TurnQueued {
			turns = append(turns, Turn{Role: "agent", Content: "", At: now, Error: errText(closedBeforeAnswer)})
		}
		if _, err := tx.Exec(ctx, `
			UPDATE test_chat SET status = 'closed', closed_at = $2, updated_at = $2,
			       turns = $3, turn_status = CASE WHEN turn_status = 'queued' THEN 'idle' ELSE turn_status END
			WHERE id = $1`, id, now, turns); err != nil {
			return fmt.Errorf("testchat: close: %w", err)
		}
		out, err = Get(ctx, tx, id)
		return err
	})
	return out, err
}

// closedBeforeAnswer is the agent-turn error for a user turn that was still
// waiting for a machine when the chat was closed.
const closedBeforeAnswer = "답을 받기 전에 시험 대화를 끝냈습니다"
