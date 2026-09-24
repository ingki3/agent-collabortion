package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The roster (openapi listRoomParticipants · addRoomParticipant ·
// updateRoomParticipant · removeRoomParticipant — FR-2.2) and the reference
// links (listRoomLinks · createRoomLink · deleteRoomLink — FR-4.5).
//
// room_participant is the one roster (0025): people and agents, and the old
// `/sessions/{id}/participants` ops write the same rows. Leaving is a
// `left_at`, not a DELETE — the lanes and tasks an agent leaves behind still
// point at it, and a person who comes back is the same row re-opened.

func (s *Server) ListRoomParticipants(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ListRoomParticipantsParams) {
	u, _, p := s.roomGate(r, roomId, rooms.ActView)
	if p != nil {
		writeProblem(w, p)
		return
	}
	includeLeft := params.IncludeLeft != nil && *params.IncludeLeft
	items, err := rooms.ListParticipants(r.Context(), s.DB, roomId, u.Id, includeLeft)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AddRoomParticipant invites a person or an agent (RoomParticipantAdd oneOf).
//
//   - person: a member of the workspace (422 not_member) — room participation
//     never exceeds the workspace. They get a `room_invited` item.
//   - agent: the inviter's FR-1.9 standing is checked exactly as the old
//     session invite did (respond_to judged on the person who pressed the
//     button). A profile whose runtime kind the room's computer does not
//     have is a warning, not a refusal (the computer may gain it).
//
// An archived room answers 409 room_archived (rooms.Deny). Inviting someone
// already here is 409 already_participant; someone who left is re-opened.
func (s *Server) AddRoomParticipant(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.AddRoomParticipantParams) {
	u, a, p := s.roomGate(r, roomId, rooms.ActInvite)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in struct {
		UserID    *uuid.UUID `json:"user_id"`
		AgentID   *uuid.UUID `json:"agent_id"`
		ProfileID *uuid.UUID `json:"profile_id"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if (in.UserID == nil) == (in.AgentID == nil) {
		writeProblem(w, apperr.Validation(apperr.Field("user_id", "one_of", "사람이나 에이전트 중 하나만 골라 주세요")))
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		now := s.Clock.Now()
		var row *rooms.ParticipantRow
		var warnings []string
		err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			var err error
			if in.UserID != nil {
				row, err = s.invitePerson(r.Context(), tx, a, u.Id, *in.UserID, now)
			} else {
				row, warnings, err = s.inviteAgent(r.Context(), tx, a, u.Id, *in.AgentID, in.ProfileID, now)
			}
			if err != nil {
				return err
			}
			rid := roomId
			objType := "user"
			objID := row.UserID
			if row.AgentID != nil {
				objType, objID = "agent", row.AgentID
			}
			if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, "participant.invited", objType, objID,
				map[string]any{"name": a.Name, "participant_id": row.ID}, now); err != nil {
				return err
			}
			api, err := rooms.ParticipantAPI(r.Context(), tx, row, u.Id)
			if err != nil {
				return err
			}
			if s.Hub != nil {
				return s.Hub.Publish(r.Context(), tx, a.WorkspaceID, &rid, "participant.joined", map[string]any{"room_id": roomId, "participant": api})
			}
			return nil
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		api, err := rooms.ParticipantAPI(r.Context(), s.DB, row, u.Id)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		// `warnings[]` rides beside the participant (openapi addRoomParticipant:
		// "runtime_kind 가 방 컴퓨터에 없으면 warnings[](거부 아님)").
		out := map[string]any{}
		raw, _ := json.Marshal(api)
		_ = json.Unmarshal(raw, &out)
		if warnings == nil {
			warnings = []string{}
		}
		out["warnings"] = warnings
		return http.StatusCreated, out, nil
	})
}

// joinRow inserts or re-opens a participant row. inserted is false when the
// row is already live (409 already_participant for the caller to raise).
func joinRow(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, userID, agentID, profileID *uuid.UUID, now time.Time) (*rooms.ParticipantRow, bool, error) {
	var conflict string
	if userID != nil {
		conflict = `(room_id, user_id) WHERE user_id IS NOT NULL`
	} else {
		conflict = `(room_id, agent_id) WHERE agent_id IS NOT NULL`
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO room_participant (room_id, user_id, agent_id, profile_id, role, joined_at)
		VALUES ($1, $2, $3, $4, 'member', $5)
		ON CONFLICT `+conflict+`
		DO UPDATE SET left_at = NULL, joined_at = EXCLUDED.joined_at, role = 'member',
		              profile_id = COALESCE(EXCLUDED.profile_id, room_participant.profile_id)
		WHERE room_participant.left_at IS NOT NULL
		RETURNING id`, roomID, userID, agentID, profileID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	row, err := rooms.GetParticipant(ctx, tx, roomID, id, false)
	return row, true, err
}

func (s *Server) invitePerson(ctx context.Context, tx pgx.Tx, a *rooms.Access, inviter, userID uuid.UUID, now time.Time) (*rooms.ParticipantRow, error) {
	var memberID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM member WHERE workspace_id = $1 AND user_id = $2`, a.WorkspaceID, userID).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Validation(apperr.Field("user_id", "not_member", "워크스페이스 멤버만 초대할 수 있습니다"))
	}
	if err != nil {
		return nil, err
	}
	row, inserted, err := joinRow(ctx, tx, a.RoomID, &userID, nil, nil, now)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, apperr.Conflict("already_participant", "이미 이 방에 있는 사람입니다")
	}
	name := displayName(ctx, tx, userID)
	if _, err := s.Router.SystemPost(ctx, tx, a.RoomID, displayName(ctx, tx, inviter)+" 님이 "+name+" 님을 방에 초대했습니다."); err != nil {
		return nil, err
	}
	// FR-8 room_invited: the invited person's inbox, as themselves (no
	// recipient_basis — they are not answering for a role). The inviter is
	// the card's actor_name (openapi 0.2.10) — kept here because it is
	// written nowhere else.
	if userID != inviter {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, actor_user_id)
			VALUES ($1, 'room_invited', $2, $3, $4, $5, $6)`,
			memberID, inbox.Severity(inbox.TypeRoomInvited), a.RoomID, row.ID, now, inviter); err != nil {
			return nil, err
		}
	}
	return row, nil
}

func (s *Server) inviteAgent(ctx context.Context, tx pgx.Tx, a *rooms.Access, inviter, agentID uuid.UUID, profileID *uuid.UUID, now time.Time) (*rooms.ParticipantRow, []string, error) {
	var owner, agentWs uuid.UUID
	var respondTo, name string
	var allow []uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT owner_id, respond_to::text, respond_to_allowlist, workspace_id, name FROM agent WHERE id = $1 AND archived_at IS NULL`, agentID).
		Scan(&owner, &respondTo, &allow, &agentWs, &name)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && agentWs != a.WorkspaceID) {
		return nil, nil, apperr.Validation(apperr.Field("agent_id", "not_found", "이 워크스페이스의 에이전트가 아닙니다"))
	}
	if err != nil {
		return nil, nil, err
	}
	// FR-1.9 on the person who pressed the button (E10-10 · 11 · 12) — the
	// same ladder the session invite and the in-room trigger gate use.
	v := tasks.MayTrigger(tasks.TriggerInput{
		RespondTo: respondTo, OwnerID: owner, Allowlist: allow, OriginatorUserID: inviter,
	})
	if !v.Allowed {
		return nil, nil, apperr.Forbidden("not_invitable", v.Reason)
	}
	var kind string
	if profileID != nil {
		if err := tx.QueryRow(ctx, `SELECT runtime_kind::text FROM agent_profile WHERE id = $1 AND agent_id = $2`, *profileID, agentID).Scan(&kind); err != nil {
			return nil, nil, apperr.Validation(apperr.Field("profile_id", "not_found", "이 에이전트의 프로파일이 아닙니다"))
		}
	} else {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id, runtime_kind::text FROM agent_profile WHERE agent_id = $1 ORDER BY is_default DESC, created_at LIMIT 1`, agentID).Scan(&id, &kind); err != nil {
			return nil, nil, apperr.Validation(apperr.Field("profile_id", "no_profile", "이 에이전트에는 프로파일이 없습니다"))
		}
		profileID = &id
	}
	row, inserted, err := joinRow(ctx, tx, a.RoomID, nil, &agentID, profileID, now)
	if err != nil {
		return nil, nil, err
	}
	if !inserted {
		return nil, nil, apperr.Conflict("already_participant", "이미 참여 중인 에이전트입니다")
	}
	if _, err := s.Router.SystemPost(ctx, tx, a.RoomID, apperr.Josa(name, "이", "가")+" 방에 참여했습니다."); err != nil {
		return nil, nil, err
	}
	warnings, err := runtimeKindWarnings(ctx, tx, a.RoomID, kind)
	return row, warnings, err
}

// runtimeKindWarnings is addRoomParticipant's non-blocking check: the room's
// computer (when one is fixed) reports no runtime of the profile's kind.
func runtimeKindWarnings(ctx context.Context, q db.DBTX, roomID uuid.UUID, kind string) ([]string, error) {
	var caps []byte
	err := q.QueryRow(ctx, `SELECT rt.capabilities FROM room r JOIN runtime rt ON rt.id = r.runtime_id WHERE r.id = $1`, roomID).Scan(&caps)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil // no computer yet — it is chosen at the first run
	}
	if err != nil {
		return nil, err
	}
	var list []struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(caps, &list)
	for _, c := range list {
		if c.Kind == kind {
			return []string{}, nil
		}
	}
	return []string{"runtime_kind_missing"}, nil
}

// UpdateRoomParticipant swaps an agent's profile for its NEXT run (FR-1.8 —
// a running attempt keeps the profile it was dispatched with).
func (s *Server) UpdateRoomParticipant(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, participantId gen.ParticipantId) {
	u, _, p := s.roomGate(r, roomId, rooms.ActConfigure)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RoomParticipantUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	var row *rooms.ParticipantRow
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		row, err = rooms.GetParticipant(r.Context(), tx, roomId, participantId, true)
		if err != nil {
			return err
		}
		if row.LeftAt != nil {
			return apperr.NotFound("participant")
		}
		if in.ProfileId == nil {
			return nil
		}
		if row.AgentID == nil {
			return apperr.Validation(apperr.Field("profile_id", "agent_only", "프로파일은 에이전트에게만 있습니다"))
		}
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM agent_profile WHERE id = $1 AND agent_id = $2)`, *in.ProfileId, *row.AgentID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return apperr.Validation(apperr.Field("profile_id", "not_found", "이 에이전트의 프로파일이 아닙니다"))
		}
		pid := uuid.UUID(*in.ProfileId)
		if _, err := tx.Exec(r.Context(), `UPDATE room_participant SET profile_id = $2 WHERE id = $1`, row.ID, pid); err != nil {
			return err
		}
		row.ProfileID = &pid
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	api, err := rooms.ParticipantAPI(r.Context(), s.DB, row, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api)
}

// RemoveRoomParticipant is 「내보내기」 and 「이 방에서 나가기」. One's own row
// needs only to be a participant; someone else's needs the steward column.
//
// Refusals (409): the room's owner (is_owner — hand the room over first) and a
// person directing an open mission of this room (is_director — hand the
// mission over first). Removing an agent cancels nothing: its lanes and
// tasks stay, only new triggers stop (the router reads live rows only).
func (s *Server) RemoveRoomParticipant(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, participantId gen.ParticipantId) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	a, err := rooms.LoadAccess(r.Context(), s.DB, roomId, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	self := a.ParticipantID == participantId
	act := rooms.ActInvite
	if self {
		act = rooms.ActLeave
	}
	if !rooms.Decide(act, a.Standing) {
		writeProblem(w, rooms.Deny(act, a.Standing))
		return
	}
	now := s.Clock.Now()
	err = s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		row, err := rooms.GetParticipant(r.Context(), tx, roomId, participantId, true)
		if err != nil {
			return err
		}
		if row.LeftAt != nil {
			return apperr.NotFound("participant")
		}
		var name string
		if row.UserID != nil {
			if row.Role == rooms.RoleOwner {
				return apperr.Conflict("is_owner", "방장은 먼저 다른 참여자에게 방장을 넘긴 뒤 나갈 수 있습니다")
			}
			var directing int
			if err := tx.QueryRow(r.Context(), `
				SELECT count(*) FROM work WHERE room_id = $1 AND director_user_id = $2
				  AND status IN ('draft', 'active', 'paused', 'completing')`, roomId, *row.UserID).Scan(&directing); err != nil {
				return err
			}
			if directing > 0 {
				pr := apperr.Conflict("is_director", "이 방에서 진행 중인 미션의 Director 입니다 — 먼저 Director 를 넘겨 주세요")
				pr.Extra = map[string]any{"works_directed": directing}
				return pr
			}
			if row.Role == rooms.RoleDeputy {
				if err := rooms.SetDeputy(r.Context(), tx, roomId, row.UserID, nil, now); err != nil {
					return err
				}
			}
			name = displayName(r.Context(), tx, *row.UserID) + " 님"
		} else {
			_ = tx.QueryRow(r.Context(), `SELECT name FROM agent WHERE id = $1`, *row.AgentID).Scan(&name)
		}
		if _, err := tx.Exec(r.Context(), `UPDATE room_participant SET left_at = $2, role = 'member' WHERE id = $1`, row.ID, now); err != nil {
			return err
		}
		line := apperr.Josa(name, "이", "가") + " 방에서 나갔습니다."
		action := "participant.left"
		if !self {
			line = displayName(r.Context(), tx, u.Id) + " 님이 " + apperr.Josa(name, "을", "를") + " 방에서 내보냈습니다."
			action = "participant.removed"
		}
		if _, err := s.Router.SystemPost(r.Context(), tx, roomId, line); err != nil {
			return err
		}
		rid := roomId
		objType, objID := "user", row.UserID
		if row.AgentID != nil {
			objType, objID = "agent", row.AgentID
		}
		if err := logActivity(r.Context(), tx, a.WorkspaceID, &rid, &u.Id, action, objType, objID,
			map[string]any{"name": a.Name, "participant_id": row.ID}, now); err != nil {
			return err
		}
		if s.Hub != nil {
			if err := s.Hub.Publish(r.Context(), tx, a.WorkspaceID, &rid, "participant.left", rooms.LeftPayload(row, now)); err != nil {
				return err
			}
		}
		s.publishRoom(r.Context(), tx, a.WorkspaceID, roomId)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Reference links (FR-4.5)
// ---------------------------------------------------------------------------

func (s *Server) ListRoomLinks(w http.ResponseWriter, r *http.Request, roomId gen.RoomId) {
	if _, _, p := s.roomGate(r, roomId, rooms.ActView); p != nil {
		writeProblem(w, p)
		return
	}
	items, err := rooms.ListLinks(r.Context(), s.DB, roomId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// notParticipantOfTarget is createRoomLink's 403. The target that does not
// exist, lives in another workspace, or that the caller is not in all get it —
// a link must not be a way to probe for rooms one does not know.
func notParticipantOfTarget() *Problem {
	return apperr.Forbidden("not_participant_of_target", "참여 중인 방만 참고 방으로 연결할 수 있습니다")
}

// CreateRoomLink lets this room's agents read `target` (FR-4.5). The person
// linking must be a steward here and a participant THERE.
func (s *Server) CreateRoomLink(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.CreateRoomLinkParams) {
	u, a, p := s.roomGate(r, roomId, rooms.ActLink)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CreateRoomLinkJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	target := uuid.UUID(in.TargetRoomId)
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		if target == roomId {
			return 0, nil, apperr.Validation(apperr.Field("target_room_id", "self", "자기 방은 참고 방으로 연결할 수 없습니다"))
		}
		t, err := rooms.LoadAccess(r.Context(), s.DB, target, u.Id)
		if err != nil || t.WorkspaceID != a.WorkspaceID || t.RoomRole == "" {
			return 0, nil, notParticipantOfTarget()
		}
		now := s.Clock.Now()
		var linkID uuid.UUID
		err = s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
			err := tx.QueryRow(r.Context(), `
				INSERT INTO room_link (room_id, target_room_id, created_by, created_at) VALUES ($1, $2, $3, $4)
				ON CONFLICT (room_id, target_room_id) DO NOTHING RETURNING id`, roomId, target, u.Id, now).Scan(&linkID)
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Conflict("already_linked", "이미 참고 방으로 연결되어 있습니다")
			}
			if err != nil {
				return err
			}
			return s.announceLink(r.Context(), tx, a, t, u.Id, linkID, "created", now)
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		out, err := rooms.LinkAPI(r.Context(), s.DB, linkID)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, out, nil
	})
}

// DeleteRoomLink unlinks. The path's room may be either end of the link and
// the caller a steward of that end ("양쪽 방 어느 쪽의 권한자든").
func (s *Server) DeleteRoomLink(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, roomLinkId gen.RoomLinkId) {
	u, a, p := s.roomGate(r, roomId, rooms.ActLink)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	err := s.inSessionTx(r.Context(), func(tx pgx.Tx) error {
		var src, dst uuid.UUID
		err := tx.QueryRow(r.Context(), `
			SELECT room_id, target_room_id FROM room_link WHERE id = $1 AND (room_id = $2 OR target_room_id = $2) FOR UPDATE`,
			roomLinkId, roomId).Scan(&src, &dst)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("room_link")
		}
		if err != nil {
			return err
		}
		srcA, dstA := a, a
		other := dst
		if roomId == dst {
			other = src
		}
		o := &rooms.Access{RoomID: other}
		if err := tx.QueryRow(r.Context(), `SELECT workspace_id, name FROM room WHERE id = $1`, other).Scan(&o.WorkspaceID, &o.Name); err != nil {
			return err
		}
		if roomId == dst {
			srcA = o
		} else {
			dstA = o
		}
		// The frame and the lines are written BEFORE the row goes: LinkAPI
		// renders the link the frame carries.
		if err := s.announceLink(r.Context(), tx, srcA, dstA, u.Id, roomLinkId, "deleted", now); err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `DELETE FROM room_link WHERE id = $1`, roomLinkId)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// announceLink writes what FR-4.5 asks for on both ends: a system line in
// each room, one activity_log entry, and `room_link.updated` to each room's
// subscribers.
func (s *Server) announceLink(ctx context.Context, tx pgx.Tx, src, dst *rooms.Access, actor, linkID uuid.UUID, action string, now time.Time) error {
	who := displayName(ctx, tx, actor)
	srcLine := who + " 님이 " + dst.Name + " 방을 참고 방으로 연결했습니다 — 이 방의 에이전트가 그 방을 읽을 수 있습니다."
	dstLine := who + " 님이 이 방을 " + src.Name + " 방의 참고 방으로 연결했습니다."
	if action == "deleted" {
		srcLine = who + " 님이 참고 방 " + dst.Name + " 연결을 풀었습니다."
		dstLine = who + " 님이 " + src.Name + " 방에서 이 방으로의 참고 연결을 풀었습니다."
	}
	if _, err := s.Router.SystemPost(ctx, tx, src.RoomID, srcLine); err != nil {
		return err
	}
	if _, err := s.Router.SystemPost(ctx, tx, dst.RoomID, dstLine); err != nil {
		return err
	}
	sid, lid := src.RoomID, linkID
	if err := logActivity(ctx, tx, src.WorkspaceID, &sid, &actor, "room_link."+action, "room_link", &lid,
		map[string]any{"name": src.Name, "target_room_id": dst.RoomID, "target_name": dst.Name}, now); err != nil {
		return err
	}
	if s.Hub == nil {
		return nil
	}
	link, err := rooms.LinkAPI(ctx, tx, linkID)
	if err != nil {
		return err
	}
	for _, room := range []uuid.UUID{src.RoomID, dst.RoomID} {
		rid := room
		if err := s.Hub.Publish(ctx, tx, src.WorkspaceID, &rid, "room_link.updated",
			map[string]any{"room_id": room, "action": action, "link": link}); err != nil {
			return err
		}
	}
	return nil
}
