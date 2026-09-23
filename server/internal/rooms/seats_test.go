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
