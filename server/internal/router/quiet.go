package router

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/quiet"
)

// T-QUIET (PRD v0.19.13 FR-2A.2.3): what one message does to its mission's
// approval-quiet state, and whether the triggers it makes are held. The
// state itself is judged by sessions.ApplyWorkEvent (quiet package doc);
// the router only applies the three message-borne transitions:
//
//   - a person's message into the mission (not a /note) re-opens it — ③;
//   - an agent's report to a person, after a person re-opened the mission,
//     closes that round and the mission is quiet again — Lead 판정
//     2026-09-28 (c);
//   - while quiet, an agent-written message's triggers are held.
//
// The caller holds the room row lock, so these read-then-writes cannot
// interleave with ApplyWorkEvent (which takes the same lock first).

// quietGate is one message's answer.
type quietGate struct {
	work *uuid.UUID
	hold bool // hold the triggers this message makes
	// changed: a state transition happened (the progress line moves).
	changed bool
	// released: lanes whose held trigger was let go (their card changes).
	released []uuid.UUID
}

// gateMessage applies the message-borne transitions and reports whether the
// message's agent-to-agent triggers are held.
func gateMessage(ctx context.Context, tx pgx.Tx, workID *uuid.UUID, author Author, content string, msgID uuid.UUID, now time.Time) (quietGate, error) {
	g := quietGate{work: workID}
	if workID == nil {
		// FR-2A.2.3 범위: only a mission's triggers. Outside any mission
		// there is no approval to wait for.
		return g, nil
	}
	st, closed, err := stateOf(ctx, tx, *workID)
	if err != nil {
		return g, err
	}
	switch author.Type {
	case "user":
		if isNote(content) || closed {
			return g, nil
		}
		if st == quiet.StateNone {
			return g, nil
		}
		// ③ a person spoke into the mission: they re-opened it.
		lanes, err := quiet.Release(ctx, tx, *workID, quiet.StateReleased, now)
		if err != nil {
			return g, err
		}
		g.released, g.changed = lanes, true
		return g, nil
	case "agent":
		if closed {
			return g, nil
		}
		if st == quiet.StateReleased {
			report, err := reportsToPerson(ctx, tx, msgID)
			if err != nil {
				return g, err
			}
			if report {
				// (c): the round a person asked for is reported back — from
				// here on the mission waits for the Director again.
				if _, err := quiet.Enter(ctx, tx, *workID, now); err != nil {
					return g, err
				}
				g.changed = true
				st = quiet.StateQuiet
			}
		}
		g.hold = st == quiet.StateQuiet
	}
	return g, nil
}

// stateOf is quiet.State plus "the mission is closed".
func stateOf(ctx context.Context, q db.DBTX, workID uuid.UUID) (string, bool, error) {
	var status string
	var st *string
	if err := q.QueryRow(ctx, `SELECT status::text, approval_quiet FROM work WHERE id = $1`, workID).Scan(&status, &st); err != nil {
		return "", false, err
	}
	if status == "completed" || status == "cancelled" {
		return quiet.StateNone, true, nil
	}
	if status != "active" || st == nil {
		return quiet.StateNone, false, nil
	}
	return *st, false, nil
}

// reportsToPerson: the message's stored speech (FR-3.1.3, decided in the
// same transaction) is a report and a person is among its addressees.
func reportsToPerson(ctx context.Context, q db.DBTX, msgID uuid.UUID) (bool, error) {
	m, err := messages.Get(ctx, q, msgID)
	if err != nil {
		return false, err
	}
	if m.Speech != "report" {
		return false, nil
	}
	for _, a := range m.Addressees {
		if a.Kind == "user" {
			return true, nil
		}
	}
	return false, nil
}

// holdsFor reports whether an agent-written trigger for a task of mission
// work is held — the mission is quiet. The task's mission is the lane's
// (bindLaneWork), which is the message's in every ordinary case; reading it
// per trigger keeps a lane bound elsewhere from borrowing the wrong answer.
//
// closed is the race FR-2A.2.3 asks to be safe: the Director's approval
// closed the mission while this turn was still writing. The room lock orders
// the two; the post that comes second finds the mission closed, and its
// trigger — which no claim would ever hand out (queue.Claim gates on an
// active mission) — is cancelled with the approval's sentence instead of
// sitting queued forever.
func holdsFor(ctx context.Context, q db.DBTX, work *uuid.UUID) (hold, closed bool, err error) {
	if work == nil {
		return false, false, nil
	}
	st, closed, err := stateOf(ctx, q, *work)
	return st == quiet.StateQuiet, closed, err
}

// publishQuiet is QuietPublish when wired.
func (s *Service) publishQuiet(ctx context.Context, q db.DBTX, wsID, roomID, workID uuid.UUID) {
	if s.QuietPublish != nil {
		s.QuietPublish(ctx, q, wsID, roomID, workID)
	}
}

// participantName is the agent's name in the room roster ("" when absent).
func participantName(ps []Participant, id uuid.UUID) string {
	for _, p := range ps {
		if p.AgentID == id {
			return p.Name
		}
	}
	return ""
}
