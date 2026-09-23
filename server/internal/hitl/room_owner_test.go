package hitl

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestAuthorizeRoomOwner is PRD v0.19 FR-2A.3's absent-owner hand-over for
// `approver_spec: room_owner` (openapi 0.2.0): the room owner at once; the
// delegate (the room's deputy, or the oldest other workspace owner —
// roomgate.Approvers) from HALF the deadline, with the instant before that;
// everyone else never, and told so with no instant (E7-11's rule).
func TestAuthorizeRoomOwner(t *testing.T) {
	owner, delegate, member, director := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	due := 24 * time.Hour
	half := 12 * time.Hour
	for _, tc := range []struct {
		name      string
		responder uuid.UUID
		delegate  uuid.UUID
		elapsed   time.Duration
		allowed   bool
		from      *time.Duration
	}{
		{"owner at once", owner, delegate, 0, true, nil},
		{"delegate before half: the instant", delegate, delegate, time.Hour, false, &half},
		{"delegate at half", delegate, delegate, half, true, nil},
		{"no delegate: a member never", member, uuid.Nil, 20 * time.Hour, false, nil},
		{"a member is not the delegate", member, delegate, 20 * time.Hour, false, nil},
		{"a mission's Director is not the room's owner", director, delegate, 20 * time.Hour, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			az := Authorize(AuthzInput{
				Spec: SpecRoomOwner, Director: director, Responder: tc.responder, IsMember: true,
				RoomOwner: owner, RoomDelegate: tc.delegate, Elapsed: tc.elapsed, DueIn: due,
			})
			if az.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v", az.Allowed, tc.allowed)
			}
			switch {
			case tc.from == nil && az.CanRespondFrom != nil:
				t.Fatalf("can_respond_from = %v, want none", *az.CanRespondFrom)
			case tc.from != nil && (az.CanRespondFrom == nil || *az.CanRespondFrom != *tc.from):
				t.Fatalf("can_respond_from = %v, want %v", az.CanRespondFrom, *tc.from)
			}
		})
	}
	if !SupportedApproverSpec(SpecRoomOwner) {
		t.Fatal("room_owner must register (openapi 0.2.0 approver_spec) — fail-closed would refuse it")
	}
}
