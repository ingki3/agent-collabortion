package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// Mission proposals (openapi createWorkProposal · listWorkProposals ·
// getWorkProposal · resolveWorkProposal — PRD FR-2A.1, §12.1-2): an agent
// proposes, a person opens. An agent opening missions itself is how a goal
// breeds goals (무한 자기 위임), so the TaskToken reaches only the proposal.

const proposalSelect = `
	SELECT p.id, p.room_id, COALESCE(p.agent_id, t.agent_id), COALESCE(a.name, ''), p.proposed_by_task_id,
	       p.goal, p.rationale, p.trigger_message_id, p.status::text, p.decided_by, p.decided_at,
	       p.reject_reason, p.work_id, p.created_at
	FROM work_proposal p
	LEFT JOIN task t ON t.id = p.proposed_by_task_id
	LEFT JOIN agent a ON a.id = COALESCE(p.agent_id, t.agent_id)`

type proposalRow struct {
	gen.WorkProposal
	decidedBy *uuid.UUID
}

func scanProposal(row pgx.Row) (*proposalRow, error) {
	var p proposalRow
	var agentID, task, trigger, work *uuid.UUID
	var decidedAt *time.Time
	var reject *string
	var status string
	if err := row.Scan(&p.Id, &p.RoomId, &agentID, &p.Agent.Name, &task, &p.Goal, &p.Rationale, &trigger, &status,
		&p.decidedBy, &decidedAt, &reject, &work, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("work_proposal")
		}
		return nil, err
	}
	if agentID != nil {
		p.Agent.Id = *agentID
	}
	p.ProposedByTaskId = task
	p.Status = gen.WorkProposalStatus(status)
	p.TriggerMessageId = nullUUID(trigger)
	p.WorkId = nullUUID(work)
	p.RejectReason = nullable.NewNullNullable[string]()
	if reject != nil {
		p.RejectReason = nullable.NewNullableWithValue(*reject)
	}
	p.DecidedAt = nullable.NewNullNullable[time.Time]()
	if decidedAt != nil {
		p.DecidedAt = nullable.NewNullableWithValue(*decidedAt)
	}
	return &p, nil
}

func nullUUID(id *uuid.UUID) nullable.Nullable[uuid.UUID] {
	if id == nil {
		return nullable.NewNullNullable[uuid.UUID]()
	}
	return nullable.NewNullableWithValue(*id)
}

// loadProposal renders one proposal (decided_by as a User).
func loadProposal(ctx context.Context, q db.DBTX, id uuid.UUID) (*gen.WorkProposal, error) {
	p, err := scanProposal(q.QueryRow(ctx, proposalSelect+` WHERE p.id = $1`, id))
	if err != nil {
		return nil, err
	}
	p.DecidedBy = nullable.NewNullNullable[gen.User]()
	if p.decidedBy != nil {
		if u, err := auth.LoadUser(ctx, q, *p.decidedBy); err == nil {
			p.DecidedBy = nullable.NewNullableWithValue(*u)
		}
	}
	out := p.WorkProposal
	return &out, nil
}

// lockOpenProposal locks a proposal of this room that is still open —
// 409 already_resolved (who and when in the Problem) otherwise.
func lockOpenProposal(ctx context.Context, tx pgx.Tx, id, roomID uuid.UUID) error {
	var status string
	var by *uuid.UUID
	var at *time.Time
	var work *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT status::text, decided_by, decided_at, work_id FROM work_proposal WHERE id = $1 AND room_id = $2 FOR UPDATE`, id, roomID).
		Scan(&status, &by, &at, &work)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("work_proposal")
	}
	if err != nil {
		return err
	}
	if status != string(gen.WorkProposalStatusOpen) {
		p := apperr.Conflict("already_resolved", "이미 처리된 제안입니다")
		p.Extra = map[string]any{"status": status, "decided_by": by, "decided_at": at, "work_id": work}
		return p
	}
	return nil
}

// acceptProposal marks an open (already locked) proposal accepted by `by`
// with the mission it opened, and takes it out of every inbox.
func (s *Server) acceptProposal(ctx context.Context, tx pgx.Tx, id, workID, by uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE work_proposal SET status = 'accepted', decided_by = $2, decided_at = $3, work_id = $4 WHERE id = $1`,
		id, by, now, workID); err != nil {
		return fmt.Errorf("work proposal accept: %w", err)
	}
	_, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, id, now)
	return err
}

// ---------------------------------------------------------------------------
// createWorkProposal — `colab work propose` (TaskToken)
// ---------------------------------------------------------------------------

func (s *Server) CreateWorkProposal(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.CreateWorkProposalParams) {
	pr := principalOf(r)
	if pr.Task == nil {
		if pr.User != nil {
			writeProblem(w, apperr.Forbidden("agent_only", "미션 제안은 에이전트만 보냅니다 — 사람은 미션을 바로 열 수 있습니다"))
			return
		}
		writeProblem(w, apperr.Unauthorized("unauthorized", "로그인이 필요합니다"))
		return
	}
	sc := pr.Task
	if p := s.commandAllowed(r, gen.ColabCommandWorkPropose); p != nil {
		writeProblem(w, p)
		return
	}
	if sc.SessionID != roomId {
		writeProblem(w, apperr.Forbidden("outside_task_scope", "다른 방에는 접근할 수 없습니다"))
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.WorkProposalCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	goal, why := strings.TrimSpace(in.Goal), strings.TrimSpace(in.Rationale)
	var errs []apperr.FieldError
	if goal == "" {
		errs = append(errs, apperr.Field("goal", "required", "제안할 목표를 적어 주세요"))
	}
	if why == "" {
		errs = append(errs, apperr.Field("rationale", "required", "왜 미션으로 열어야 하는지 적어 주세요"))
	}
	if len(errs) > 0 {
		writeProblem(w, apperr.Validation(errs...))
		return
	}
	s.idempotent(r.Context(), w, taskScope(sc.TaskID), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		now := s.Clock.Now()
		var id, wsID uuid.UUID
		err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			var archived bool
			if err := tx.QueryRow(r.Context(), `SELECT workspace_id, status = 'archived' FROM room WHERE id = $1`, roomId).Scan(&wsID, &archived); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return apperr.NotFound("room")
				}
				return err
			}
			if archived {
				return apperr.Conflict("room_archived", rooms.RoomArchivedDetail)
			}
			var trigger *uuid.UUID
			if err := tx.QueryRow(r.Context(), `SELECT trigger_message_id FROM task WHERE id = $1`, sc.TaskID).Scan(&trigger); err != nil {
				return err
			}
			if err := tx.QueryRow(r.Context(), `
				INSERT INTO work_proposal (room_id, proposed_by_task_id, agent_id, goal, rationale, trigger_message_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
				roomId, sc.TaskID, sc.AgentID, goal, why, trigger, now).Scan(&id); err != nil {
				return fmt.Errorf("work proposal: %w", err)
			}
			// FR-8 받은 요청 work_proposed: every person in the room may open it
			// (openapi resolveWorkProposal "방 참여자(사람) 누구나").
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
				SELECT m.id, $3::inbox_item_type, $4::inbox_severity, $1, $2, $5
				FROM room_participant p JOIN room r ON r.id = p.room_id
				JOIN member m ON m.workspace_id = r.workspace_id AND m.user_id = p.user_id
				WHERE p.room_id = $1 AND p.user_id IS NOT NULL AND p.left_at IS NULL`,
				roomId, id, inbox.TypeWorkProposed, inbox.Severity(inbox.TypeWorkProposed), now); err != nil {
				return fmt.Errorf("work proposal inbox: %w", err)
			}
			if s.Hub != nil {
				out, err := loadProposal(r.Context(), tx, id)
				if err != nil {
					return err
				}
				rid := roomId
				_ = s.Hub.Publish(r.Context(), tx, wsID, &rid, "work_proposal.created", map[string]any{"room_id": roomId, "proposal": out})
			}
			return nil
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		// No `status` row on the attempt's feed yet: task_event.schema.json's
		// verb list is closed and has no proposal verb — `work propose` joins
		// ColabCommand and the §2.5 table in R3 (colab-cli.md v0.7), and the
		// feed row belongs with that change.
		out, err := loadProposal(r.Context(), s.DB, id)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, out, nil
	})
}

// ---------------------------------------------------------------------------
// list · get
// ---------------------------------------------------------------------------

func (s *Server) ListWorkProposals(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ListWorkProposalsParams) {
	if _, _, p := s.roomGate(r, roomId, rooms.ActProposals); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	where := []string{"p.room_id = $1"}
	args := []any{roomId}
	if params.Status != nil {
		args = append(args, string(*params.Status))
		where = append(where, fmt.Sprintf("p.status::text = $%d", len(args)))
	}
	if params.Cursor != nil {
		if cid, err := uuid.Parse(*params.Cursor); err == nil {
			args = append(args, cid)
			where = append(where, fmt.Sprintf("(p.created_at, p.id) < (SELECT created_at, id FROM work_proposal WHERE id = $%d)", len(args)))
		}
	}
	args = append(args, limit+1)
	rows, err := s.DB.Query(r.Context(), `SELECT p.id FROM work_proposal p WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY p.created_at DESC, p.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeErr(w, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	var next *string
	if len(ids) > limit {
		ids = ids[:limit]
		c := ids[len(ids)-1].String()
		next = &c
	}
	items := make([]gen.WorkProposal, 0, len(ids))
	for _, id := range ids {
		p, err := loadProposal(r.Context(), s.DB, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		items = append(items, *p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// proposalGate is the proposal's room + ActProposals (a proposal in a room
// the caller cannot see is 404 like one that does not exist).
func (s *Server) proposalGate(r *http.Request, id uuid.UUID, act rooms.Action) (*gen.User, *rooms.Access, *Problem) {
	var roomID uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT room_id FROM work_proposal WHERE id = $1`, id).Scan(&roomID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, apperr.NotFound("work_proposal")
		}
		return nil, nil, apperr.Internal(err)
	}
	u, a, p := s.roomGate(r, roomID, act)
	if p != nil && p.Status == http.StatusNotFound {
		return nil, nil, apperr.NotFound("work_proposal")
	}
	return u, a, p
}

func (s *Server) GetWorkProposal(w http.ResponseWriter, r *http.Request, workProposalId gen.WorkProposalId) {
	if _, _, p := s.proposalGate(r, workProposalId, rooms.ActProposals); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := loadProposal(r.Context(), s.DB, workProposalId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// resolveWorkProposal — 열기 · 거절
// ---------------------------------------------------------------------------

// ResolveWorkProposal: opening makes the PERSON who opens it the Director (not
// the agent that proposed — FR-2A.1); refusing tells the agent on the
// timeline. A proposal decided already is 409 already_resolved.
func (s *Server) ResolveWorkProposal(w http.ResponseWriter, r *http.Request, workProposalId gen.WorkProposalId, params gen.ResolveWorkProposalParams) {
	u, a, p := s.proposalGate(r, workProposalId, rooms.ActProposals)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.WorkProposalResolution
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	accept, errA := in.AsWorkProposalResolution0()
	reject, errR := in.AsWorkProposalResolution1()
	action := ""
	switch {
	case errA == nil && string(accept.Action) == "accept":
		action = "accept"
	case errR == nil && string(reject.Action) == "reject":
		action = "reject"
	default:
		writeProblem(w, apperr.Validation(apperr.Field("action", "enum", "열기(accept) 또는 거절(reject) 중 하나를 골라 주세요")))
		return
	}
	if action == "accept" && !rooms.Decide(rooms.ActOpenWork, a.Standing) {
		writeProblem(w, rooms.Deny(rooms.ActOpenWork, a.Standing))
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		now := s.Clock.Now()
		var workID *uuid.UUID
		err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			if err := lockOpenProposal(r.Context(), tx, workProposalId, a.RoomID); err != nil {
				return err
			}
			prop, err := loadProposal(r.Context(), tx, workProposalId)
			if err != nil {
				return err
			}
			if action == "accept" {
				wc := gen.WorkCreate{Goal: prop.Goal}
				if accept.Work != nil {
					wc = *accept.Work
					if strings.TrimSpace(wc.Goal) == "" {
						wc.Goal = prop.Goal
					}
				}
				// 연 사람이 Director — never the room's default, never the agent.
				director := u.Id
				wc.DirectorUserId = &director
				wc.FromProposalId = &workProposalId
				id, err := s.openWork(r.Context(), tx, a, u, wc, now)
				if err != nil {
					return err
				}
				workID = &id
			} else {
				reason := ""
				if reject.Reason != nil {
					reason = strings.TrimSpace(*reject.Reason)
				}
				var rr *string
				if reason != "" {
					rr = &reason
				}
				if _, err := tx.Exec(r.Context(), `
					UPDATE work_proposal SET status = 'rejected', decided_by = $2, decided_at = $3, reject_reason = $4 WHERE id = $1`,
					workProposalId, u.Id, now, rr); err != nil {
					return err
				}
				if _, err := tx.Exec(r.Context(), `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, workProposalId, now); err != nil {
					return err
				}
				line := displayName(r.Context(), tx, u.Id) + " 님이 " + prop.Agent.Name + " 의 미션 제안 「" + firstLineOf(prop.Goal) + "」을 거절했습니다."
				if reason != "" {
					line += " 사유: " + reason
				}
				if _, err := s.Router.SystemPost(r.Context(), tx, a.RoomID, line); err != nil {
					return err
				}
			}
			rid := a.RoomID
			if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "work_proposal."+map[string]string{"accept": "accepted", "reject": "rejected"}[action],
				"work_proposal", &workProposalId, map[string]any{"goal": prop.Goal, "work_id": workID}, now); err != nil {
				return err
			}
			if s.Hub != nil {
				_ = s.Hub.Publish(r.Context(), tx, a.WorkspaceID, &rid, "work_proposal.resolved", map[string]any{
					"room_id": a.RoomID, "proposal_id": workProposalId, "action": action, "work_id": workID,
				})
			}
			return nil
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		s.Queue.Notifier.Notify()
		out := map[string]any{}
		prop, err := loadProposal(r.Context(), s.DB, workProposalId)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		out["proposal"] = prop
		if workID != nil {
			wk, err := sessions.LoadWork(r.Context(), s.DB, *workID, u.Id)
			if err != nil {
				return 0, nil, apperr.As(err)
			}
			out["work"] = wk
		}
		return http.StatusOK, out, nil
	})
}
