package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestBlockedDetailNextApprover is openapi 0.2.8: getRoom's banner names the
// person who can answer from delegate_at, and which link of the FR-2A.3
// chain they are — before half the deadline only, from the same judgement
// that picks approver and delegate_at.
func TestBlockedDetailNextApprover(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, t0)
	room := seed.SessionID
	if _, err := pool.Exec(ctx, `UPDATE room SET blocked_reason = 'budget', blocked_detail = '{}' WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, proposed_default, approver_spec, purpose, due_at, created_at)
		VALUES ($1, NULL, 'system', 'approval', '예산', NULL, 'room_owner', 'budget', $2, $3)`,
		room, t0.Add(24*time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	mkUser := func(email, wsRole string, at time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ($1, $1, $2) RETURNING id`, email, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`, seed.WorkspaceID, id, wsRole, at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	load := func(now time.Time) gen.BlockedDetail {
		t.Helper()
		a, err := LoadAccess(ctx, pool, room, seed.UserID)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Load(ctx, pool, a, now)
		if err != nil {
			t.Fatal(err)
		}
		d, err := r.BlockedDetail.Get()
		if err != nil {
			t.Fatalf("blocked_detail absent: %v", err)
		}
		return d
	}
	next := func(d gen.BlockedDetail) (uuid.UUID, string) {
		t.Helper()
		var id uuid.UUID
		var role string
		if u, err := d.NextApprover.Get(); err == nil {
			id = uuid.UUID(u.Id)
		} else if !d.NextApprover.IsNull() {
			t.Fatalf("next_approver unset, want a value or an explicit null")
		}
		if r, err := d.NextApproverRole.Get(); err == nil {
			role = string(r)
		} else if !d.NextApproverRole.IsNull() {
			t.Fatalf("next_approver_role unset, want a value or an explicit null")
		}
		return id, role
	}

	// Sole workspace owner: nobody to hand to.
	if id, role := next(load(t0.Add(time.Hour))); id != uuid.Nil || role != "" {
		t.Fatalf("sole owner: next = %s/%q, want null/null", id, role)
	}

	older := mkUser("o2@example.com", "owner", t0.Add(time.Minute))
	if id, role := next(load(t0.Add(time.Hour))); id != older || role != string(gen.BlockedDetailNextApproverRoleWorkspaceOwner) {
		t.Fatalf("no deputy: next = %s/%q, want the oldest other owner %s as workspace_owner", id, role, older)
	}
	deputy := mkUser("dep@example.com", "member", t0)
	if _, err := pool.Exec(ctx, `UPDATE room SET deputy_owner_user_id = $2 WHERE id = $1`, room, deputy); err != nil {
		t.Fatal(err)
	}
	d := load(t0.Add(time.Hour))
	if id, role := next(d); id != deputy || role != string(gen.BlockedDetailNextApproverRoleRoomDeputy) {
		t.Fatalf("deputy: next = %s/%q, want %s as room_deputy", id, role, deputy)
	}
	if at, err := d.DelegateAt.Get(); err != nil || !at.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("delegate_at = %v (%v), want +12h", at, err)
	}
	if u, err := d.Approver.Get(); err != nil || uuid.UUID(u.Id) != seed.UserID {
		t.Fatalf("approver = %v, want the owner before half", u)
	}

	// After half: the deputy answers; nobody further.
	d = load(t0.Add(13 * time.Hour))
	if id, role := next(d); id != uuid.Nil || role != "" {
		t.Fatalf("after half: next = %s/%q, want null/null", id, role)
	}
	if u, err := d.Approver.Get(); err != nil || uuid.UUID(u.Id) != deputy {
		t.Fatalf("after half approver = %v, want the deputy", u)
	}
}
