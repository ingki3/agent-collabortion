package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// SweepHitlDeadlines is FR-5.4's "기한 만료 시 동작", run by the scheduler.
//
// The rule is a grid of TYPE × autonomy and the type decides first: a
// `question` under `autonomous` proceeds with the agent's proposal, while an
// `approval` or an `info` request ALWAYS keeps waiting, in every autonomy.
// There is no auto-approve and no auto-reject — approving empties the human
// gate the request exists to be, and rejecting kills healthy work (M7, E7-14,
// E7-21).
//
// `expired` is not a status: an overdue request stays `open` and carries a
// flag, so a late answer is still an answer (E7-15).
//
// Returns how many requests it touched.
func (s *Server) SweepHitlDeadlines(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	type due struct {
		id       uuid.UUID
		session  uuid.UUID
		task     *uuid.UUID
		kind     string
		autonomy string
		def      string
		question string
	}
	rows, err := s.DB.Query(ctx, `
		SELECT h.id, h.session_id, h.task_id, h.type::text, s.autonomy::text,
		       COALESCE(h.proposed_default, ''), h.question
		FROM hitl_request h JOIN session s ON s.id = h.session_id
		WHERE h.status = 'open' AND h.due_at <= $1
		ORDER BY h.due_at`, now)
	if err != nil {
		return 0, err
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.session, &d.task, &d.kind, &d.autonomy, &d.def, &d.question); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, d := range list {
		p := hitl.PlanExpiry(d.kind, d.autonomy, d.def)
		err := s.inSessionTx(ctx, func(tx pgx.Tx) error {
			var status string
			if err := tx.QueryRow(ctx, `SELECT status::text FROM hitl_request WHERE id = $1 FOR UPDATE`, d.id).Scan(&status); err != nil {
				return err
			}
			if status != hitl.StatusOpen {
				return nil // answered between the scan and the lock
			}
			if p.Status == hitl.StatusOpen {
				// Overdue is a FLAG, and the inbox sorts it to the top.
				_, err := tx.Exec(ctx, `UPDATE hitl_request SET overdue = true WHERE id = $1 AND NOT overdue`, d.id)
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE hitl_request SET status = 'auto_answered', answer = $2, answered_at = $3
				WHERE id = $1`, d.id, p.Answer, now); err != nil {
				return err
			}
			// E7-12: the decision is marked automatic. "The Director said 투자자"
			// and "nobody answered and we used the agent's proposal" read
			// identically in the log without it.
			if _, err := insertDecision(ctx, tx, d.session,
				d.question+" → "+p.Answer, "기한 만료로 에이전트 제안대로 진행", "hitl", &d.id, true, now); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE inbox_item SET read_at = COALESCE(read_at, $2) WHERE ref_id = $1`, d.id, now)
			return err
		})
		if err != nil {
			return n, err
		}
		if p.TaskStatus == "queued" && d.task != nil {
			if _, err := s.Tasks.ResumeFromHuman(ctx, *d.task, tasks.CauseHitlAnswer); err != nil {
				return n, err
			}
			s.Queue.Notifier.Notify()
		}
		n++
	}
	return n, nil
}

var _ = time.Now
