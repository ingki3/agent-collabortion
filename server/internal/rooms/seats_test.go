package rooms

import (
	"testing"

	"github.com/google/uuid"
)

// TestSuccessor is §12.1-4: the oldest workspace owner who is not the person
// leaving.
func TestSuccessor(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name    string
		owners  []uuid.UUID
		leaving uuid.UUID
		want    uuid.UUID
		ok      bool
	}{
		{"방장이 owner 가 아니면 최고참 owner", []uuid.UUID{a, b}, c, a, true},
		{"떠나는 사람이 최고참 owner 면 다음 owner", []uuid.UUID{a, b}, a, b, true},
		{"owner 가 떠나는 사람뿐이면 없다", []uuid.UUID{a}, a, uuid.Nil, false},
		{"owner 0명", nil, a, uuid.Nil, false},
	}
	for _, tc := range cases {
		got, ok := Successor(tc.owners, tc.leaving)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: Successor = %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestDirectorSuccessor is FR-5.3's "Director 가 워크스페이스를 떠나면 방장이
// Director 를 승계" — and when the Director IS the room owner, the room's
// successor takes both seats.
func TestDirectorSuccessor(t *testing.T) {
	owner, dir, next := uuid.New(), uuid.New(), uuid.New()
	if got := DirectorSuccessor(owner, dir, next); got != owner {
		t.Errorf("Director leaves → room owner %v, got %v", owner, got)
	}
	if got := DirectorSuccessor(owner, owner, next); got != next {
		t.Errorf("owner-Director leaves → room successor %v, got %v", next, got)
	}
}

// TestRoomSuccessor is openapi 0.2.3 removeMember: the room's deputy first,
// the oldest workspace owner otherwise — and never the person leaving, even
// when they held the deputy seat of a room they also own (impossible by the
// seat rules, but the function must not hand the room back to them).
func TestRoomSuccessor(t *testing.T) {
	leaving, dep, o1, o2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		deputy *uuid.UUID
		want   uuid.UUID
	}{
		{"부방장이 있으면 부방장", &dep, dep},
		{"부방장이 없으면 ws owner 최고참", nil, o1},
		{"떠나는 사람이 부방장 칸에 있으면 건너뛴다", &leaving, o1},
	} {
		got, ok := RoomSuccessor(tc.deputy, []uuid.UUID{leaving, o1, o2}, leaving)
		if !ok || got != tc.want {
			t.Errorf("%s: got %v ok=%v, want %v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := RoomSuccessor(nil, []uuid.UUID{leaving}, leaving); ok {
		t.Error("no owner left: ok must be false")
	}
}
