package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Session roster (openapi addParticipant · updateParticipant ·
// removeParticipant, FR-1.5, FR-1.9, O2).

func (s *Server) AddParticipant(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, wsID, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		AgentID   uuid.UUID  `json:"agent_id"`
		ProfileID *uuid.UUID `json:"profile_id"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var owner uuid.UUID
		var respondTo string
		var allow []uuid.UUID
		var agentWs uuid.UUID
		var name string
		if err := tx.QueryRow(r.Context(), `
			SELECT owner_id, respond_to::text, respond_to_allowlist, workspace_id, name FROM agent WHERE id = $1`, in.AgentID).
			Scan(&owner, &respondTo, &allow, &agentWs, &name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Validation(apperr.Field("agent_id", "not_found", "그런 에이전트가 없습니다"))
			}
			return err
		}
		if agentWs != wsID {
			return apperr.Validation(apperr.Field("agent_id", "other_workspace", "다른 워크스페이스의 에이전트입니다"))
		}
		// FR-1.9, judged on the ORIGINATOR — the human who pressed the button,
		// never a service identity. This is the same ladder the in-session
		// trigger gate uses (E10-10 · E10-11 · E10-12).
		v := tasks.MayTrigger(tasks.TriggerInput{
			RespondTo: respondTo, OwnerID: owner, Allowlist: allow,
			InSession: false, Participant: false, OriginatorUserID: u.Id,
		})
		if !v.Allowed {
			return apperr.Forbidden("not_invitable", v.Reason)
		}
		profileID := in.ProfileID
		if profileID == nil {
			var id uuid.UUID
			if err := tx.QueryRow(r.Context(), `
				SELECT id FROM agent_profile WHERE agent_id = $1 ORDER BY is_default DESC, created_at LIMIT 1`, in.AgentID).Scan(&id); err != nil {
				return apperr.Validation(apperr.Field("profile_id", "no_profile", "이 에이전트에는 프로파일이 없습니다"))
			}
			profileID = &id
		}
		tag, err := tx.Exec(r.Context(), `
			INSERT INTO session_participant (session_id, agent_id, profile_id, joined_at)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, sessionId, in.AgentID, *profileID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.Conflict("already_participant", "이미 참여 중인 에이전트입니다")
		}
		_, err = s.Router.SystemPost(r.Context(), tx, sessionId, name+"이(가) 세션에 참여했습니다.")
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := sessions.ListParticipants(r.Context(), s.DB, sessionId)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, pt := range out {
		if pt.AgentId == in.AgentID {
			writeJSON(w, http.StatusCreated, pt)
			return
		}
	}
	writeProblem(w, apperr.NotFound("participant"))
}

func (s *Server) UpdateParticipant(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, agentId gen.AgentId) {
	_, _, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		ProfileID *uuid.UUID `json:"profile_id"`
		Assignee  *bool      `json:"assignee"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(), `
			SELECT EXISTS (SELECT 1 FROM session_participant WHERE session_id = $1 AND agent_id = $2)`, sessionId, agentId).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound("participant")
		}
		if in.ProfileID != nil {
			var ok bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM agent_profile WHERE id = $1 AND agent_id = $2)`,
				*in.ProfileID, agentId).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return apperr.Validation(apperr.Field("profile_id", "not_found", "이 에이전트의 프로파일이 아닙니다"))
			}
			// FR-1.8: the swap applies to the NEXT run. Running attempts keep
			// the profile they were dispatched with — changing it mid-turn
			// would price the turn at a model that did not run it.
			if _, err := tx.Exec(r.Context(), `
				UPDATE session_participant SET profile_id = $3 WHERE session_id = $1 AND agent_id = $2`,
				sessionId, agentId, *in.ProfileID); err != nil {
				return err
			}
		}
		if in.Assignee != nil && *in.Assignee {
			if _, err := tx.Exec(r.Context(), `UPDATE session SET assignee_agent_id = $2, updated_at = $3 WHERE id = $1`,
				sessionId, agentId, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := sessions.ListParticipants(r.Context(), s.DB, sessionId)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, pt := range out {
		if pt.AgentId == agentId {
			writeJSON(w, http.StatusOK, pt)
			return
		}
	}
	writeProblem(w, apperr.NotFound("participant"))
}

func (s *Server) RemoveParticipant(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, agentId gen.AgentId) {
	_, _, p := s.sessionControl(r, sessionId, false)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var assignee *uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT assignee_agent_id FROM session WHERE id = $1`, sessionId).Scan(&assignee); err != nil {
			return err
		}
		if assignee != nil && *assignee == agentId {
			// Removing the assignee leaves the session with nobody to hand the
			// initial task to (E16-A step 1).
			return apperr.Conflict("assignee_participant", "assignee는 제거할 수 없습니다 — 먼저 다른 assignee를 지정하세요")
		}
		var live int
		if err := tx.QueryRow(r.Context(), `
			SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2
			  AND status IN ('queued', 'running', 'waiting_human', 'paused')`, sessionId, agentId).Scan(&live); err != nil {
			return err
		}
		if live > 0 {
			// O2: the lane's workdir and its open question belong to this
			// agent. Removing it would strand both.
			pr := apperr.Conflict("running_lanes", "진행 중인 lane이 있습니다 — 먼저 끝내거나 중단하세요")
			pr.Extra = map[string]any{"running_lane_count": live}
			return pr
		}
		var name string
		_ = tx.QueryRow(r.Context(), `SELECT name FROM agent WHERE id = $1`, agentId).Scan(&name)
		tag, err := tx.Exec(r.Context(), `DELETE FROM session_participant WHERE session_id = $1 AND agent_id = $2`, sessionId, agentId)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("participant")
		}
		_, err = s.Router.SystemPost(r.Context(), tx, sessionId, name+"이(가) 세션에서 제외되었습니다.")
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = now
	w.WriteHeader(http.StatusNoContent)
}
