package workdirs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// daemon-protocol v0.10.0 §6.1 — the path rule table, one assertion per row.
// These are what the bundle, the `<folders>` block, GC and S13 all read; the
// integration tests (httpapi folders_test.go) check the same strings on the
// wire.

var (
	roomID  = uuid.MustParse("3f2a91c0-1111-4222-8333-444455556666")
	workID  = uuid.MustParse("8b11de02-aaaa-4bbb-8ccc-ddddeeeeffff")
	agentID = uuid.MustParse("0c7e5d19-9999-4888-8777-666655554444")
)

func TestPathSlugKeepsHangulAndFollowsTheContract(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Game Studio", "game-studio"},
		{"게임 제작", "게임-제작"},
		{"  --스네이크!! v2 --", "스네이크-v2"},
		{"snake_prototype", "snake_prototype"},
		{"", "x"},
		{"!!!", "x"},
		// NFD 한글(맥 파일 이름) → NFC 로 합쳐 같은 조각이 된다.
		{"\u1100\u1161\u11b7", "감"},
		// 40 runes, then trailing `-` trimmed again.
		{strings.Repeat("가", 39) + " 나", strings.Repeat("가", 39)},
		{strings.Repeat("ab", 30), strings.Repeat("ab", 20)},
	} {
		if got := PathSlug(c.in); got != c.want {
			t.Errorf("PathSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The branch keeps the ASCII Slug — Hangul still falls to `x` there.
	if got := Slug("게임 제작"); got != "x" {
		t.Errorf("Slug(게임 제작) = %q — the branch slug must stay ASCII (§6.1)", got)
	}
}

// TestPathSlugParityTable reads the table the web's pathSlug test reads too
// (web/lib/workdir-tree.test.ts): the rule is implemented twice (server paths,
// S13's 「만들 때의 이름」 badge), and a Korean agent name must give the same
// leaf on both sides — rune cut, not byte cut (PR #345 review).
func TestPathSlugParityTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "path_slug_parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []struct{ In, Want string } `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) < 3 {
		t.Fatalf("parity table has %d cases — it would compare nothing", len(table.Cases))
	}
	for _, c := range table.Cases {
		got := PathSlug(c.In)
		if got != c.Want {
			t.Errorf("PathSlug(%q) = %q, want %q", c.In, got, c.Want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("PathSlug(%q) = %q is not valid UTF-8", c.In, got)
		}
	}
}

func TestPathRuleTable(t *testing.T) {
	const root = "/Users/x/.colab"
	w := workID
	o := Owner{RoomID: roomID, RoomName: "게임 제작", WorkID: &w, WorkTitle: "Snake Prototype",
		AgentID: agentID, AgentName: "Developer", Role: RoleAgent}
	rows := []struct {
		name, got, want string
	}{
		{"mission agent", PlanDir(root, o, IDLen), root + "/rooms/게임-제작-3f2a91c0/snake-prototype-8b11de02/developer-0c7e5d19"},
		{"mission _shared", PlanDir(root, Owner{RoomID: roomID, RoomName: "게임 제작", WorkID: &w, WorkTitle: "Snake Prototype", Role: RoleShared}, IDLen),
			root + "/rooms/게임-제작-3f2a91c0/snake-prototype-8b11de02/_shared"},
		{"outside a mission", PlanDir(root, Owner{RoomID: roomID, RoomName: "게임 제작", AgentID: agentID, AgentName: "Developer", Role: RoleAgent}, IDLen),
			root + "/rooms/게임-제작-3f2a91c0/_room/developer-0c7e5d19"},
		{"worktree checkout", PlanWorktreePath(root, o, IDLen), root + "/rooms/게임-제작-3f2a91c0/_worktrees/developer-0c7e5d19"},
		{"collision → 12", PlanDir(root, o, IDLenLong), root + "/rooms/게임-제작-3f2a91c01111/snake-prototype-8b11de02aaaa/developer-0c7e5d199999"},
		{"branch", WorktreeBranch("게임 제작", roomID, "Developer"), "colab/x-3f2a91c0/developer"},
		{"branch ascii room", WorktreeBranch("Game Studio", roomID, "Developer"), "colab/game-studio-3f2a91c0/developer"},
	}
	for _, r := range rows {
		if r.got != r.want {
			t.Errorf("%s:\n got %s\nwant %s", r.name, r.got, r.want)
		}
	}
	// A shared row outside a mission has no place (§4.1: only a mission turn).
	if p := PlanDir(root, Owner{RoomID: roomID, RoomName: "r", Role: RoleShared}, IDLen); p != "" {
		t.Errorf("_shared outside a mission = %q, want none", p)
	}
	// No root, no path — never a relative one (S-55).
	if p := PlanDir("", o, IDLen); p != "" {
		t.Errorf("no root → %q", p)
	}
	if p := PlanDir("rel/root", o, IDLen); p != "" {
		t.Errorf("relative root → %q", p)
	}
	// Same for a checkout — the worktree half's inner layer of the root guard
	// (the outer one is queue.planBundleWorkdir, httpapi folders_layers_test).
	if p := PlanWorktreePath("", o, IDLen); p != "" {
		t.Errorf("worktree, no root → %q", p)
	}
	if p := PlanWorktreePath("rel/root", o, IDLen); p != "" {
		t.Errorf("worktree, relative root → %q", p)
	}
	// Reserved pieces never equal a name piece (which always ends in -<id>).
	for _, name := range []string{"_room", "_shared", "_worktrees"} {
		if Piece(name, agentID, IDLen) == name {
			t.Errorf("a name piece equals the reserved %q", name)
		}
	}
}

// DB rows — EnsureDirRow: one row per (room, mission, agent), made on first
// use, fixed path, 12 digits on a collision with another owner's row.
func TestEnsureDirRowOwnerKeyFixedPathAndCollision(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s := testdb.Plant(t, pool, now)
	if _, err := pool.Exec(ctx, `UPDATE room SET runtime_id = $2, name = '게임 제작' WHERE id = $1`, s.SessionID, s.RuntimeID); err != nil {
		t.Fatal(err)
	}
	var wk uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT legacy_work_id FROM room WHERE id = $1`, s.SessionID).Scan(&wk); err != nil || wk == uuid.Nil {
		t.Fatalf("seed mission: %v", err)
	}
	o := Owner{RoomID: s.SessionID, RoomName: "게임 제작", WorkID: &wk, WorkTitle: "스네이크", AgentID: s.AgentID, AgentName: "Lead", Role: RoleAgent}
	laneB := uuid.New()
	a, err := EnsureDirRow(ctx, pool, s.RuntimeID, "/Users/x/.colab", o, nil, now)
	if err != nil || !a.Created || !strings.HasPrefix(a.Path, "/Users/x/.colab/rooms/게임-제작-") {
		t.Fatalf("first row = %+v %v", a, err)
	}
	// Same owner, another lane, the room and the mission renamed: same row,
	// same path (D3 A · §6.1 만들 때 고정).
	o2 := o
	o2.RoomName, o2.WorkTitle, o2.AgentName = "완전히 다른 방", "다른 미션", "Renamed"
	b, err := EnsureDirRow(ctx, pool, s.RuntimeID, "/Users/x/.colab", o2, nil, now)
	_ = laneB
	if err != nil || b.ID != a.ID || b.Path != a.Path || b.Created {
		t.Fatalf("second lookup = %+v, want the first row %+v unchanged", b, a)
	}
	// Collision: another owner's row already holds the 8-digit path this owner
	// would get → 12 digits.
	other := Owner{RoomID: s.SessionID, RoomName: "게임 제작", AgentID: s.AgentID, AgentName: "Lead", Role: RoleAgent}
	eight := PlanDir("/Users/x/.colab", other, IDLen)
	// The squatter is a row of ANOTHER owner (the mission's, not outside it)
	// that happens to hold that exact path.
	if _, err := pool.Exec(ctx, `INSERT INTO workdir (session_id, work_id, agent_id, role, kind, path_or_ref) VALUES ($1, $2, $3, 'shared', 'dir', $4)`,
		s.SessionID, wk, nil, eight); err != nil {
		t.Fatal(err)
	}
	c, err := EnsureDirRow(ctx, pool, s.RuntimeID, "/Users/x/.colab", other, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Path != PlanDir("/Users/x/.colab", other, IDLenLong) {
		t.Errorf("colliding path = %s, want the 12-digit plan %s", c.Path, PlanDir("/Users/x/.colab", other, IDLenLong))
	}
	// No root → ErrNoRoot (none is refused like worktree, §4.1 v0.10.0).
	o3 := o
	o3.AgentID = uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO agent (id, workspace_id, name, role, role_description, instructions, owner_id) VALUES ($1, $2, 'X', 'writer', 'd', 'i', $3)`, o3.AgentID, s.WorkspaceID, s.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDirRow(ctx, pool, s.RuntimeID, "", o3, nil, now); err != ErrNoRoot {
		t.Errorf("no root = %v, want ErrNoRoot", err)
	}
}

// D8 B — the mission-folder clock.
func TestMissionFolderDisposable(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		open  bool
		since time.Duration
		days  int
		want  bool
	}{
		{true, 400 * day, 14, false}, // open mission: never, at any age
		{false, 0, 14, false},        // just closed: not yet (D8 B — close is not a trigger)
		{false, 13 * day, 14, false},
		{false, 14 * day, 14, true},
		{false, 2 * day, 1, true},
		{false, 20 * day, -1, true}, // unset → default 14
	} {
		if got := missionFolderDisposable(c.open, c.since, c.days); got != c.want {
			t.Errorf("missionFolderDisposable(open=%v, since=%v, days=%d) = %v, want %v", c.open, c.since, c.days, got, c.want)
		}
	}
}
