package rooms

import (
	"strings"
	"testing"
)

// TestDecideTable is SCREEN §2.3 「방 층」 × PRD FR-5.3, row by row. Each row
// is one (caller, room) shape and the set of actions it may take; every
// action NOT listed must be refused. Writing the allowed set out in full (not
// "everything but delete") is the point: a new Action added to Decide without
// a row here fails the "unlisted" half.
func TestDecideTable(t *testing.T) {
	all := []Action{ActView, ActPost, ActInvite, ActConfigure, ActLink, ActBlock, ActArchive,
		ActDelete, ActTransferOwner, ActSetDeputy, ActSummarize, ActLeave, ActMarkRead, ActSubscribe,
		ActOpenWork, ActProposals}
	steward := []Action{ActView, ActPost, ActInvite, ActConfigure, ActLink, ActBlock, ActArchive, ActSummarize}
	cases := []struct {
		name  string
		f     Standing
		allow []Action
	}{
		// ── 방장: everything.
		{"방장 · workspace 방", Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisWorkspace},
			append(append([]Action{}, steward...), ActDelete, ActTransferOwner, ActSetDeputy, ActLeave, ActMarkRead, ActSubscribe, ActOpenWork, ActProposals)},
		{"방장 · invited 방", Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisInvited},
			append(append([]Action{}, steward...), ActDelete, ActTransferOwner, ActSetDeputy, ActLeave, ActMarkRead, ActSubscribe, ActOpenWork, ActProposals)},
		// ── 부방장: the steward set, never delete / hand over / appoint (FR-5.3 부방장 행).
		{"부방장", Standing{WorkspaceRole: "member", RoomRole: RoleDeputy, Visibility: VisInvited},
			append(append([]Action{}, steward...), ActLeave, ActMarkRead, ActSubscribe, ActOpenWork, ActProposals)},
		// ── 방 참여자(사람): read, post, summarise, leave.
		{"참여자 · invited 방", Standing{WorkspaceRole: "member", RoomRole: RoleMember, Visibility: VisInvited},
			[]Action{ActView, ActPost, ActSummarize, ActLeave, ActMarkRead, ActSubscribe, ActOpenWork, ActProposals}},
		// A workspace ADMIN who is also a plain participant has the admin column.
		{"참여자인 ws admin", Standing{WorkspaceRole: "admin", RoomRole: RoleMember, Visibility: VisInvited},
			append(append([]Action{}, steward...), ActDelete, ActTransferOwner, ActSetDeputy, ActLeave, ActMarkRead, ActSubscribe, ActOpenWork, ActProposals)},
		// ── ws owner·admin, not a participant: audit view of every room, the
		// admin column of the table, but posting only after joining (invited).
		{"ws owner · 비참여 · invited 방(감사)", Standing{WorkspaceRole: "owner", Visibility: VisInvited},
			[]Action{ActView, ActInvite, ActConfigure, ActLink, ActBlock, ActArchive, ActDelete, ActTransferOwner, ActSetDeputy, ActSummarize}},
		{"ws admin · 비참여 · workspace 방", Standing{WorkspaceRole: "admin", Visibility: VisWorkspace},
			append(append([]Action{}, steward...), ActDelete, ActTransferOwner, ActSetDeputy)},
		// ── ws member, not a participant.
		{"ws member · 비참여 · workspace 방", Standing{WorkspaceRole: "member", Visibility: VisWorkspace},
			[]Action{ActView, ActPost, ActSummarize}},
		{"ws member · 비참여 · invited 방", Standing{WorkspaceRole: "member", Visibility: VisInvited}, nil},
		// ── not a workspace member: nothing, even with a stale room row.
		{"워크스페이스 밖", Standing{WorkspaceRole: "", RoomRole: RoleOwner, Visibility: VisWorkspace}, nil},
		// ── archived: new activity closed, the past and its stewards kept.
		{"방장 · 보관된 방", Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisWorkspace, Archived: true},
			[]Action{ActView, ActConfigure, ActArchive, ActDelete, ActTransferOwner, ActSetDeputy, ActLeave, ActMarkRead, ActSubscribe, ActProposals}},
		{"참여자 · 보관된 방", Standing{WorkspaceRole: "member", RoomRole: RoleMember, Visibility: VisWorkspace, Archived: true},
			[]Action{ActView, ActLeave, ActMarkRead, ActSubscribe, ActProposals}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := map[Action]bool{}
			for _, a := range c.allow {
				want[a] = true
			}
			var wrong []string
			for _, a := range all {
				if got := Decide(a, c.f); got != want[a] {
					wrong = append(wrong, string(a))
				}
			}
			if len(wrong) > 0 {
				t.Errorf("Decide disagrees with the table on: %s", strings.Join(wrong, ", "))
			}
		})
	}
}

// TestCapabilitiesFollowDecide pins Room.my_capabilities to Decide — the
// buttons S7/S20 enable must be exactly what the server then accepts.
func TestCapabilitiesFollowDecide(t *testing.T) {
	cases := []struct {
		f    Standing
		want string
	}{
		{Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisWorkspace},
			"post,invite,configure,link,block,archive,delete,transfer_owner,summarize"},
		{Standing{WorkspaceRole: "member", RoomRole: RoleDeputy, Visibility: VisWorkspace},
			"post,invite,configure,link,block,archive,summarize"},
		{Standing{WorkspaceRole: "member", RoomRole: RoleMember, Visibility: VisWorkspace}, "post,summarize"},
		{Standing{WorkspaceRole: "owner", Visibility: VisInvited},
			"invite,configure,link,block,archive,delete,transfer_owner,summarize"},
		{Standing{WorkspaceRole: "member", Visibility: VisInvited}, ""},
		{Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisWorkspace, Archived: true},
			"configure,archive,delete,transfer_owner"},
	}
	for _, c := range cases {
		if got := strings.Join(Capabilities(c.f), ","); got != c.want {
			t.Errorf("%+v: capabilities = %q, want %q", c.f, got, c.want)
		}
	}
}

// TestDenyShapes: an invisible room is 404 (existence hidden), an archived
// room answers 409 room_archived for what it closes, everything else is a 403
// whose code names the missing role.
func TestDenyShapes(t *testing.T) {
	cases := []struct {
		name   string
		a      Action
		f      Standing
		status int
		code   string
	}{
		{"invited 방을 모르는 멤버", ActView, Standing{WorkspaceRole: "member", Visibility: VisInvited}, 404, "not_found"},
		{"워크스페이스 밖", ActView, Standing{Visibility: VisWorkspace}, 404, "not_found"},
		{"참여자가 초대", ActInvite, Standing{WorkspaceRole: "member", RoomRole: RoleMember, Visibility: VisWorkspace}, 403, "room_steward_required"},
		{"부방장이 삭제", ActDelete, Standing{WorkspaceRole: "member", RoomRole: RoleDeputy, Visibility: VisWorkspace}, 403, "room_owner_required"},
		{"감사 열람자가 게시", ActPost, Standing{WorkspaceRole: "owner", Visibility: VisInvited}, 403, "not_participant"},
		{"보관된 방에 초대", ActInvite, Standing{WorkspaceRole: "member", RoomRole: RoleOwner, Visibility: VisWorkspace, Archived: true}, 409, "room_archived"},
		{"보관된 방에 참여자가 초대(권한 없음이 먼저)", ActInvite, Standing{WorkspaceRole: "member", RoomRole: RoleMember, Visibility: VisWorkspace, Archived: true}, 403, "room_steward_required"},
	}
	for _, c := range cases {
		p := Deny(c.a, c.f)
		if p.Status != c.status || p.Code != c.code {
			t.Errorf("%s: Deny = %d %s, want %d %s", c.name, p.Status, p.Code, c.status, c.code)
		}
	}
}
