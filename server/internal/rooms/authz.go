// Package rooms is the room half of PRD v0.19 (FR-2 · FR-2.2 · FR-4.5 links ·
// FR-5.3 · FR-8 unread): who may do what in a room, the room list in one
// query, the roster (people and agents in one table), reference links and the
// automatic owner succession of §12.1-4.
//
// The permission table is a pure function (Decide) so that every handler asks
// the same question the same way and the table can be tested without a
// database — SCREEN §2.3 「방 층」 is one table, and eight handlers each
// re-deriving "방장·부방장·ws owner·admin" is how one of them ends up letting
// a deputy delete the room.
package rooms

import (
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// Action is one row of SCREEN §2.3 「방 층」.
type Action string

const (
	// ActView reads the room: its timeline, roster, links, settings.
	ActView Action = "view"
	// ActPost posts a message (and so does everything that posts on the
	// caller's behalf — mention, reply).
	ActPost Action = "post"
	// ActInvite adds a person or an agent, or removes someone else.
	ActInvite Action = "invite"
	// ActConfigure changes room settings (updateRoom) and a participant's
	// profile.
	ActConfigure Action = "configure"
	// ActLink creates or removes a reference-room link.
	ActLink Action = "link"
	// ActBlock is 「이 방 멈춤」·해제 (blocked_reason manual).
	ActBlock Action = "block"
	// ActArchive is archive and unarchive (same people, FR-2.4 [V19-C]).
	ActArchive Action = "archive"
	// ActDelete deletes the room for good (FR-2.6).
	ActDelete Action = "delete"
	// ActTransferOwner hands the room to another participant.
	ActTransferOwner Action = "transfer_owner"
	// ActSetDeputy appoints or clears the one deputy (§12.1-4 — S19 and S20
	// use the same permission).
	ActSetDeputy Action = "set_deputy"
	// ActSummarize is 「여기까지 정리」 (FR-2.5) — a reading aid, not limited.
	ActSummarize Action = "summarize"
	// ActLeave is 「이 방에서 나가기」 on one's own row. The owner may leave only
	// after handing the room over — that refusal is a 409 (is_owner), not a
	// 403, because the button is the owner's and the answer is "먼저 넘기세요".
	ActLeave Action = "leave"
	// ActMarkRead moves the caller's own unread marker (a person's row only).
	ActMarkRead Action = "mark_read"
	// ActSubscribe is the caller's own notification switch for a sub-mission.
	ActSubscribe Action = "subscribe"
)

// Room roles (RoomRole). "" = not a (current) participant.
const (
	RoleOwner  = "owner"
	RoleDeputy = "deputy"
	RoleMember = "member"
)

// Visibility (RoomVisibility).
const (
	VisWorkspace = "workspace"
	VisInvited   = "invited"
)

// Standing is everything Decide reads. The handler loads them once; Decide never
// touches the database.
type Standing struct {
	// WorkspaceRole is the caller's member.role: owner · admin · member, or ""
	// when the caller is not a member of the room's workspace at all.
	WorkspaceRole string
	// RoomRole is the caller's live room_participant row (left_at IS NULL):
	// owner · deputy · member, or "".
	RoomRole string
	// Visibility is the room's.
	Visibility string
	// Archived is room.status = archived: new activity is closed, the past
	// is kept (FR-2.4 [V19-C]).
	Archived bool
}

func (f Standing) wsAdmin() bool {
	return f.WorkspaceRole == "owner" || f.WorkspaceRole == "admin"
}

// steward is the 방장·부방장·ws owner·admin column group — the people who run
// the room day to day (FR-5.3 부방장 행 · V19-GAP P-A).
func (f Standing) steward() bool {
	return f.RoomRole == RoleOwner || f.RoomRole == RoleDeputy || f.wsAdmin()
}

// head is 방장·ws owner·admin — delete, hand over, appoint the deputy.
func (f Standing) head() bool {
	return f.RoomRole == RoleOwner || f.wsAdmin()
}

// Decide is SCREEN §2.3 「방 층」 × FR-5.3. It answers whether the caller may
// take the action at all; state conflicts (the owner leaving, a Director
// leaving, active tasks at archive time) are the handler's 409s.
func Decide(a Action, f Standing) bool {
	if f.WorkspaceRole == "" {
		// Room participation never exceeds workspace membership (FR-2.2).
		return false
	}
	// "열람": a participant, a workspace owner·admin (audit), or — in a
	// workspace-visible room — any member (v0.18 continuity, FR-5.3 [v0.19]).
	view := f.RoomRole != "" || f.wsAdmin() || f.Visibility == VisWorkspace
	if !view {
		return false
	}
	// Posting is "참여 뒤" for an auditing owner·admin: a workspace-visible
	// room is open to every member, an invited one only to its participants.
	post := f.RoomRole != "" || f.Visibility == VisWorkspace
	switch a {
	case ActView:
		return true
	case ActPost:
		return post && !f.Archived
	case ActSummarize:
		// SCREEN §2.3: all four columns ✅ — it posts a message, so the
		// archived room refuses it like any other post.
		return (post || f.wsAdmin()) && !f.Archived
	case ActInvite, ActLink, ActBlock:
		return f.steward() && !f.Archived
	case ActConfigure, ActArchive:
		return f.steward()
	case ActDelete, ActTransferOwner, ActSetDeputy:
		return f.head()
	case ActLeave:
		return f.RoomRole != ""
	case ActMarkRead, ActSubscribe:
		return f.RoomRole != ""
	}
	return false
}

// Capabilities is Room.my_capabilities — the buttons S7/S20 enable. The enum
// is the contract's; the order is fixed so two reads compare equal.
func Capabilities(f Standing) []string {
	out := []string{}
	for _, c := range []struct {
		name string
		act  Action
	}{
		{"post", ActPost}, {"invite", ActInvite}, {"configure", ActConfigure}, {"link", ActLink},
		{"block", ActBlock}, {"archive", ActArchive}, {"delete", ActDelete},
		{"transfer_owner", ActTransferOwner}, {"summarize", ActSummarize},
	} {
		if Decide(c.act, f) {
			out = append(out, c.name)
		}
	}
	return out
}

// Deny is the 403 (or 404) for a refused action. A caller who cannot even see
// the room gets 404 — an invited room does not exist for the uninvited
// (FR-5.3, "목록·검색에서도 숨는다"). Archived rooms answer 409 room_archived
// for the actions an archived room closes, because the caller COULD do it in
// an active room and the fix is to unarchive.
func Deny(a Action, f Standing) *apperr.Problem {
	if f.WorkspaceRole == "" || !Decide(ActView, f) {
		return apperr.NotFound("room")
	}
	if f.Archived {
		unarchived := f
		unarchived.Archived = false
		if Decide(a, unarchived) {
			return apperr.Conflict("room_archived", RoomArchivedDetail)
		}
	}
	switch a {
	case ActPost, ActSummarize:
		return apperr.Forbidden("not_participant", "이 방의 참여자만 할 수 있습니다 — 방장에게 초대를 요청해 주세요")
	case ActDelete, ActTransferOwner, ActSetDeputy:
		return apperr.Forbidden("room_owner_required", "방장이나 소유자·관리자만 할 수 있습니다")
	case ActLeave, ActMarkRead, ActSubscribe:
		return apperr.Forbidden("not_participant", "이 방의 참여자가 아닙니다")
	}
	return apperr.Forbidden("room_steward_required", "방장·부방장이나 소유자·관리자만 할 수 있습니다")
}

// RoomArchivedDetail is the 409 room_archived sentence (addRoomParticipant and
// every action an archived room closes).
const RoomArchivedDetail = "보관된 방입니다 — 먼저 보관을 해제해 주세요"
