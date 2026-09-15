package testchat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/cost"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// ---------------------------------------------------------------------------
// daemon-protocol §4.2 · §4.4, routed here when task_id is a test chat
// ---------------------------------------------------------------------------

// current checks the daemon's (id, attempt) against the turn in flight.
func current(r *Row, attempt int) error {
	if attempt != r.TurnNo {
		return ErrStaleAttempt
	}
	if r.TurnStatus == TurnIdle || r.TurnStatus == TurnQueued {
		// idle: the turn already ended (finish is idempotent — the caller
		// decides); queued: never handed out, so nothing can report on it.
		return ErrInvalidTransition
	}
	return nil
}

// Phase is §4.2 `phase` for a test chat turn: dispatched → preparing → running.
func (s *Service) Phase(ctx context.Context, id uuid.UUID, attempt int, phase string, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := current(r, attempt); err != nil {
			return err
		}
		switch phase {
		case TurnPreparing:
			if r.TurnStatus == TurnRunning {
				return ErrInvalidTransition
			}
		case TurnRunning:
		default:
			return ErrInvalidTransition
		}
		_, err = tx.Exec(ctx, `
			UPDATE test_chat SET turn_status = $2, started_at = COALESCE(started_at, $3), heartbeat_at = $3, updated_at = $3
			WHERE id = $1`, id, phase, now)
		return err
	})
}

// Heartbeat is §4.2 `heartbeat`: liveness plus the turn's running usage.
// Nothing here fails on the preview — the caller publishes it (SSE
// test_chat.delta) after this returns, exactly as the task path does.
func (s *Service) Heartbeat(ctx context.Context, id uuid.UUID, attempt int, usage contracts.Usage, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := current(r, attempt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE test_chat SET heartbeat_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
			return err
		}
		if !usageEmpty(usage) {
			if _, err := tx.Exec(ctx, `UPDATE test_chat SET turn_usage = $2 WHERE id = $1`, id, usage); err != nil {
				return err
			}
		}
		return nil
	})
}

// Ingest is §4.2 `events` for a test chat: nothing is stored as task_event
// (§4.5 "활동 피드가 없으므로 저장하지 않고"). Two classes are consumed —
// `message.say` (turn-level, not partial; kind text) becomes the agent turn's
// body, `usage.report` refreshes the turn's usage. `(id, attempt, seq)`
// idempotency holds through turn_last_seq: a re-sent batch changes nothing.
// Returns accepted_seq_max.
func (s *Service) Ingest(ctx context.Context, id uuid.UUID, attempt int, evs []contracts.TaskEvent, now time.Time) (int, error) {
	var maxSeq int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := current(r, attempt); err != nil {
			return err
		}
		maxSeq = r.TurnLastSeq
		text := r.TurnText
		var usage *contracts.Usage
		for _, e := range evs {
			if e.Seq <= r.TurnLastSeq {
				continue
			}
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
			switch {
			case e.Class == "message" && e.Verb == "say" && !e.Partial:
				if kind, _ := e.Payload["kind"].(string); kind != "" && kind != "text" {
					continue
				}
				if t, _ := e.Payload["text"].(string); t != "" {
					if text != "" {
						text += "\n"
					}
					text += t
				}
			case e.Class == "usage" && e.Verb == "report":
				u := usageFromPayload(e.Payload)
				if !usageEmpty(u) {
					usage = &u
				}
			}
		}
		if maxSeq == r.TurnLastSeq && text == r.TurnText && usage == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE test_chat SET turn_text = $2, turn_last_seq = $3, heartbeat_at = $4, updated_at = $4,
			       turn_usage = COALESCE($5, turn_usage)
			WHERE id = $1`, id, text, maxSeq, now, usage); err != nil {
			return fmt.Errorf("testchat: ingest: %w", err)
		}
		return nil
	})
	return maxSeq, err
}

func usageFromPayload(p map[string]any) contracts.Usage {
	raw, _ := json.Marshal(p)
	var u contracts.Usage
	_ = json.Unmarshal(raw, &u)
	return u
}

func usageEmpty(u contracts.Usage) bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CostUSD == 0
}

// FinishResult is what the daemon handler publishes after Finish committed.
type FinishResult struct {
	Chat *Row
	Turn Turn
	// Repeated is true when this attempt had already been closed (finish is
	// idempotent per attempt, §4.4) — nothing to publish.
	Repeated bool
}

// Finish is §4.4 for a test chat turn: the agent turn is confirmed from the
// folded message.say text, usage priced and added to the chat's totals
// (workspace cost only, FR-1.8.1), the runtime session ref stored for the next
// turn's `resume`, and `transport` recorded (§4.5). A `failed` outcome leaves
// the turn with `error` in the user's words; the chat stays open.
func (s *Service) Finish(ctx context.Context, id uuid.UUID, attempt int, f contracts.Finish, now time.Time) (*FinishResult, error) {
	res := &FinishResult{}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := lock(ctx, tx, id)
		if err != nil {
			return err
		}
		if attempt != r.TurnNo {
			return ErrStaleAttempt
		}
		if r.TurnStatus == TurnIdle {
			res.Chat, res.Repeated = r, true
			return nil
		}
		if r.TurnStatus == TurnQueued {
			return ErrInvalidTransition
		}
		turn := Turn{Role: "agent", Content: r.TurnText, At: now}
		switch f.Outcome {
		case "completed":
		case "cancelled":
			turn.Error = errText(FailureText(contracts.FailCancelled))
		case "paused_budget":
			turn.Error = errText(budgetStopped)
		default: // failed
			kind := f.FailureKind
			if kind == "" {
				kind = contracts.FailOther
			}
			turn.Error = errText(FailureText(kind))
		}
		usage := f.Usage
		if usageEmpty(usage) && r.TurnUsage != nil {
			// An empty finish usage is "no information" (S-19): the turn's
			// running usage from heartbeats is what it cost.
			usage = *r.TurnUsage
		}
		var ref *contracts.RuntimeSessionRef
		if f.RuntimeSessionRef != nil && f.RuntimeSessionRef.SessionID != "" && f.RuntimeSessionRef.RuntimeKind != "" {
			ref = f.RuntimeSessionRef
		}
		var transport *string
		if f.Transport != "" {
			t := string(f.Transport)
			transport = &t
		}
		turn, err = s.priceAndClose(ctx, tx, r, turn, usage, ref, transport, now)
		if err != nil {
			return err
		}
		res.Chat, err = Get(ctx, tx, id)
		res.Turn = turn
		return err
	})
	return res, err
}

// priceAndClose is the one place a turn ends: it prices the usage, appends the
// agent turn, folds tokens and cost into the chat, stores ref/transport, and
// puts the row back to idle so the next user turn can be posted.
func (s *Service) priceAndClose(ctx context.Context, tx pgx.Tx, r *Row, turn Turn, usage contracts.Usage,
	ref *contracts.RuntimeSessionRef, transport *string, now time.Time) (Turn, error) {
	usd, estimated := s.price(ctx, tx, r, usage)
	if !usageEmpty(usage) {
		turn.Usage = &TurnUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	}
	if err := closeTurn(ctx, tx, r, turn, &turnCost{in: usage.InputTokens, out: usage.OutputTokens, usd: usd, estimated: estimated}, now); err != nil {
		return turn, err
	}
	if ref != nil {
		if _, err := tx.Exec(ctx, `UPDATE test_chat SET runtime_session_ref = $2 WHERE id = $1`, r.ID, ref); err != nil {
			return turn, fmt.Errorf("testchat: runtime_session_ref: %w", err)
		}
	}
	if transport != nil {
		if _, err := tx.Exec(ctx, `UPDATE test_chat SET transport = $2 WHERE id = $1`, r.ID, *transport); err != nil {
			return turn, fmt.Errorf("testchat: transport: %w", err)
		}
	}
	return turn, nil
}

// price applies the session rule (harness v0.7.1 §11, S-20): a measured cost
// is taken as reported; `estimated` — or a zero cost with tokens behind it — is
// priced from the workspace table at the model the daemon measured, falling
// back to the profile's model. An unknown model stays $0 and `estimated`,
// which is the honest answer (cost.Table.Estimate).
func (s *Service) price(ctx context.Context, q db.DBTX, r *Row, u contracts.Usage) (float64, bool) {
	if usageEmpty(u) {
		return 0, false
	}
	if !u.Estimated && u.CostUSD > 0 {
		return u.CostUSD, false
	}
	table, err := cost.Load(ctx, q, r.WorkspaceID)
	if err != nil {
		return 0, true
	}
	model := u.Model
	if model == "" {
		_ = q.QueryRow(ctx, `SELECT model FROM agent_profile WHERE id = $1`, r.ProfileID).Scan(&model)
	}
	usd, ok := table.Estimate(model, u.InputTokens, u.OutputTokens, u.CacheReadTokens)
	if !ok {
		return 0, true
	}
	return usd, true
}

type turnCost struct {
	in, out   int64
	usd       float64
	estimated bool
}

// closeTurn appends the agent turn and returns the row to idle. `c` nil means
// the turn never reached a machine (no usage to add).
func closeTurn(ctx context.Context, tx pgx.Tx, r *Row, turn Turn, c *turnCost, now time.Time) error {
	turns := append(r.Turns, turn)
	if c == nil {
		c = &turnCost{}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE test_chat SET turns = $2, turn_status = 'idle', heartbeat_at = NULL, turn_text = '', turn_usage = NULL,
		       input_tokens = input_tokens + $3, output_tokens = output_tokens + $4, cost_usd = cost_usd + $5,
		       estimated = estimated OR $6, updated_at = $7
		WHERE id = $1`, r.ID, turns, c.in, c.out, c.usd, c.estimated, now); err != nil {
		return fmt.Errorf("testchat: close turn: %w", err)
	}
	return nil
}

// Expired is one turn ExpireStale closed, for the caller to publish.
type Expired struct {
	Chat *Row
	Turn Turn
}

// ExpireStale applies §4.1's 5-minute and §4.2's 3-minute bounds to test chat
// turns — WITHOUT requeueing (§4.5 "재큐잉하지 않는다 — 그 턴을 error 로
// 닫는다"). Called from the scheduler sweep next to tasks.ExpireStale.
func (s *Service) ExpireStale(ctx context.Context, now time.Time) ([]Expired, error) {
	var out []Expired
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, CASE WHEN turn_status = 'running' THEN 'running' ELSE 'preparing' END FROM test_chat
			WHERE (turn_status IN ('dispatched', 'preparing') AND dispatched_at < $1)
			   OR (turn_status = 'running' AND COALESCE(heartbeat_at, started_at, dispatched_at) < $2)
			FOR UPDATE SKIP LOCKED`, now.Add(-contracts.DispatchedTimeout), now.Add(-contracts.HeartbeatExpiry))
		if err != nil {
			return err
		}
		type hit struct {
			id   uuid.UUID
			kind string
		}
		var hits []hit
		for rows.Next() {
			var h hit
			if err := rows.Scan(&h.id, &h.kind); err != nil {
				rows.Close()
				return err
			}
			hits = append(hits, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range hits {
			r, err := Get(ctx, tx, h.id)
			if err != nil {
				return err
			}
			turn := Turn{Role: "agent", Content: r.TurnText, At: now, Error: errText(preparingTimeout)}
			if h.kind == "running" {
				turn.Error = errText(runningTimeout)
			}
			var usage contracts.Usage
			if r.TurnUsage != nil {
				usage = *r.TurnUsage
			}
			turn, err = s.priceAndClose(ctx, tx, r, turn, usage, nil, nil, now)
			if err != nil {
				return err
			}
			after, err := Get(ctx, tx, h.id)
			if err != nil {
				return err
			}
			out = append(out, Expired{Chat: after, Turn: turn})
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// sentences a person reads (COMPONENTS §8.4; wording lock sinkFuncs)
// ---------------------------------------------------------------------------

const (
	preparingTimeout = "컴퓨터가 5분 안에 준비를 마치지 못해 이 답을 기다리지 않습니다 — 컴퓨터의 연결과 로그인 상태를 확인해 주세요"
	runningTimeout   = "컴퓨터의 응답이 3분 동안 없어 이 답을 기다리지 않습니다 — 컴퓨터가 켜져 있고 연결돼 있는지 확인해 주세요"
	budgetStopped    = "이 에이전트의 할 일당 예산에 닿아 답을 멈췄습니다"
)

// errText is how a turn's `error` is set — the one call the wording lock
// follows (sinkHelpers) to the sentence behind it.
func errText(s string) *string { return &s }

// FailureText is `failure_kind` in the user's words for a test chat turn's
// `error` (§4.5 "failed 면 그 턴의 error 에 failure_kind 를 §8.4 문장으로").
func FailureText(kind contracts.FailureKind) string {
	switch kind {
	case contracts.FailAuth:
		return "컴퓨터의 로그인이 만료되었습니다 — 그 컴퓨터에서 다시 로그인한 뒤 시도해 주세요"
	case contracts.FailQuota:
		return "사용량 한도에 닿아 답하지 못했습니다"
	case contracts.FailRateLimited:
		return "요청이 잠시 제한되어 답하지 못했습니다 — 조금 뒤 다시 시도해 주세요"
	case contracts.FailConfig:
		return "프로파일 설정이 맞지 않아 실행하지 못했습니다 — 모델과 옵션을 확인해 주세요"
	case contracts.FailNetwork:
		return "네트워크 문제로 연결이 끊겨 답하지 못했습니다"
	case contracts.FailRuntimeOffline:
		return "컴퓨터 연결이 끊겨 답하지 못했습니다"
	case contracts.FailStall:
		return "에이전트가 3분 넘게 아무 출력도 내지 않아 멈췄습니다"
	case contracts.FailTimeout:
		return "제한 시간 안에 끝나지 않아 멈췄습니다"
	case contracts.FailCancelled:
		return "사람이 중단했습니다"
	}
	return "알 수 없는 이유로 실행이 끝났습니다 — 컴퓨터의 기록을 확인해 주세요"
}
