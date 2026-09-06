// Package tokens issues and verifies the per-attempt task token
// (`ctk_…`, COLAB_TASK_TOKEN — contracts/colab-cli.md §1, daemon-protocol.md §5).
//
// Only the sha256 of a token is stored. Revocation is server → daemon: Revoke
// marks the row and queues a `revoke` command for the runtime that holds the
// attempt (daemon-protocol §4.3, §5). A revoked token answers every CLI call
// with 401 token_revoked (FR-9.1, E11-04).
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

const (
	Prefix = "ctk_"
	// TTL bounds an attempt's token even if the server never sees it finish.
	TTL = 7 * 24 * time.Hour
)

var (
	ErrInvalid = errors.New("tokens: invalid task token")
	ErrRevoked = errors.New("tokens: task token revoked")
	ErrExpired = errors.New("tokens: task token expired")
)

// Scope is what a valid token grants (colab-cli.md §1: task, attempt, lane,
// session, agent) plus the runtime that holds the attempt.
type Scope struct {
	TaskID    uuid.UUID
	Attempt   int
	LaneID    uuid.UUID
	SessionID uuid.UUID
	AgentID   uuid.UUID
	RuntimeID *uuid.UUID
	ExpiresAt time.Time
}

type Service struct {
	Clock clock.Clock
}

func New(c clock.Clock) *Service { return &Service{Clock: c} }

// Generate returns a fresh token and its hash. Exported for tests.
func Generate() (token, hash string) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	token = Prefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, Hash(token)
}

// Hash is sha256 hex of the token string.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue creates the token for (task, attempt). Any earlier token of the same
// attempt is replaced (claim of a requeued attempt issues a new one).
func (s *Service) Issue(ctx context.Context, q db.DBTX, sc Scope) (string, error) {
	token, hash := Generate()
	now := s.Clock.Now()
	_, err := q.Exec(ctx, `
		INSERT INTO task_token (task_id, attempt, token_hash, lane_id, session_id, agent_id, runtime_id, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (task_id, attempt) DO UPDATE
		SET token_hash = EXCLUDED.token_hash, runtime_id = EXCLUDED.runtime_id,
		    issued_at = EXCLUDED.issued_at, expires_at = EXCLUDED.expires_at,
		    revoked_at = NULL, revoke_reason = NULL`,
		sc.TaskID, sc.Attempt, hash, sc.LaneID, sc.SessionID, sc.AgentID, sc.RuntimeID, now, now.Add(TTL))
	if err != nil {
		return "", fmt.Errorf("tokens: issue: %w", err)
	}
	return token, nil
}

// Verify resolves a bearer token to its scope. Errors: ErrInvalid (unknown or
// malformed), ErrRevoked, ErrExpired.
func (s *Service) Verify(ctx context.Context, q db.DBTX, token string) (*Scope, error) {
	if !strings.HasPrefix(token, Prefix) || len(token) < len(Prefix)+16 {
		return nil, ErrInvalid
	}
	var sc Scope
	var revokedAt *time.Time
	err := q.QueryRow(ctx, `
		SELECT task_id, attempt, lane_id, session_id, agent_id, runtime_id, expires_at, revoked_at
		FROM task_token WHERE token_hash = $1`, Hash(token)).
		Scan(&sc.TaskID, &sc.Attempt, &sc.LaneID, &sc.SessionID, &sc.AgentID, &sc.RuntimeID, &sc.ExpiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("tokens: verify: %w", err)
	}
	if revokedAt != nil {
		return nil, ErrRevoked
	}
	if !s.Clock.Now().Before(sc.ExpiresAt) {
		return nil, ErrExpired
	}
	return &sc, nil
}

// Revoke invalidates the token of (task, attempt) and queues a `revoke`
// command for the runtime that holds it (daemon-protocol §5). Idempotent.
func (s *Service) Revoke(ctx context.Context, q db.DBTX, taskID uuid.UUID, attempt int, reason string) error {
	now := s.Clock.Now()
	var runtimeID *uuid.UUID
	var already bool
	err := q.QueryRow(ctx, `
		UPDATE task_token
		SET revoked_at = COALESCE(revoked_at, $3), revoke_reason = COALESCE(revoke_reason, $4)
		WHERE task_id = $1 AND attempt = $2
		RETURNING runtime_id, (revoked_at <> $3)`, taskID, attempt, now, reason).
		Scan(&runtimeID, &already)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // never issued (task expired while queued/dispatched without claim)
	}
	if err != nil {
		return fmt.Errorf("tokens: revoke: %w", err)
	}
	if already || runtimeID == nil {
		return nil
	}
	return QueueCommand(ctx, q, *runtimeID, contracts.Command{Type: contracts.CmdRevoke, TaskID: taskID.String(), Attempt: attempt})
}

// CommandTTL is the §4.3 common consumption bound: a command nobody consumed
// within 24h is dropped and recorded on the feed ("명령 미소비 만료").
const CommandTTL = 24 * time.Hour

// QueueCommand appends a server → daemon command for runtimeID. It is handed
// out by every claim / events / heartbeat response until its effect is
// observed (daemon-protocol §4.3 v0.2) — see PendingCommands.
func QueueCommand(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, cmd contracts.Command) error {
	var taskID, sessionID *uuid.UUID
	if id, err := uuid.Parse(cmd.TaskID); err == nil {
		taskID = &id
	}
	if id, err := uuid.Parse(cmd.SessionID); err == nil {
		sessionID = &id
	}
	var attempt *int
	if cmd.Attempt > 0 {
		attempt = &cmd.Attempt
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO daemon_command (runtime_id, type, payload, task_id, attempt, session_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		runtimeID, string(cmd.Type), cmd, taskID, attempt, sessionID); err != nil {
		return fmt.Errorf("tokens: queue command: %w", err)
	}
	return nil
}

// PendingCommands returns every unconsumed command of the runtime. Nothing is
// marked on read: delivery is at-least-once and a command stays in every
// response until its effect is observed (daemon-protocol §4.3 table):
//
//	cancel          finish of that attempt arrived
//	revoke          finish of that attempt arrived, or HeartbeatExpiry since issue
//	probe           the next probe was received
//	gc              the workdir report no longer lists the workdirs
//	rebind_prepare  the new attempt reported phase: preparing
//	(all)           CommandTTL since issue
//
// The finish/probe/report/phase conditions are recorded by the Consume*
// functions; the two time bounds are applied here and swept by ExpireCommands.
func PendingCommands(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, now time.Time) ([]contracts.Command, error) {
	rows, err := q.Query(ctx, `
		SELECT id, payload FROM daemon_command
		WHERE runtime_id = $1 AND consumed_at IS NULL
		  AND created_at > $2
		  AND NOT (type = 'revoke' AND created_at <= $3)
		ORDER BY id`, runtimeID, now.Add(-CommandTTL), now.Add(-contracts.HeartbeatExpiry))
	if err != nil {
		return nil, fmt.Errorf("tokens: pending commands: %w", err)
	}
	defer rows.Close()
	out := []contracts.Command{}
	var ids []int64
	for rows.Next() {
		var id int64
		var c contracts.Command
		if err := rows.Scan(&id, &c); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// S-35: `delivered_at` is the FIRST time this command rode a response, and
	// it is distinct from `consumed_at`. Delivery is at-least-once and a
	// response can be lost, so being handed out does NOT consume a command —
	// but without the timestamp there is no way to tell "the daemon has been
	// told and has not acted" from "the daemon has never seen it", and the
	// §4.3 at-least-once guarantee (E11-05) cannot be evidenced. Nothing here
	// reads it back into a decision; it is the audit trail the re-delivery
	// rule is judged by. COALESCE keeps the first delivery, not the last.
	if len(ids) > 0 {
		if _, err := q.Exec(ctx, `
			UPDATE daemon_command SET delivered_at = COALESCE(delivered_at, $2) WHERE id = ANY($1)`, ids, now); err != nil {
			return nil, fmt.Errorf("tokens: mark delivered: %w", err)
		}
	}
	return out, nil
}

// ConsumeAttemptCommands marks the cancel/revoke commands of (task, attempt)
// consumed: the daemon's finish for that attempt arrived (§4.3).
func ConsumeAttemptCommands(ctx context.Context, q db.DBTX, taskID uuid.UUID, attempt int, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE daemon_command SET consumed_at = $3, consumed_by = 'finish'
		WHERE task_id = $1 AND attempt = $2 AND type IN ('cancel', 'revoke') AND consumed_at IS NULL`, taskID, attempt, now)
	if err != nil {
		return fmt.Errorf("tokens: consume attempt commands: %w", err)
	}
	return nil
}

// ConsumeProbeCommands marks the runtime's probe commands consumed: a probe
// arrived after they were issued.
func ConsumeProbeCommands(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE daemon_command SET consumed_at = $2, consumed_by = 'probe'
		WHERE runtime_id = $1 AND type = 'probe' AND consumed_at IS NULL AND created_at <= $2`, runtimeID, now)
	if err != nil {
		return fmt.Errorf("tokens: consume probe commands: %w", err)
	}
	return nil
}

// ConsumeRebindCommands marks rebind_prepare for (runtime, session) consumed:
// the new attempt reported phase: preparing.
func ConsumeRebindCommands(ctx context.Context, q db.DBTX, runtimeID, sessionID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE daemon_command SET consumed_at = $3, consumed_by = 'phase_preparing'
		WHERE runtime_id = $1 AND session_id = $2 AND type = 'rebind_prepare' AND consumed_at IS NULL`, runtimeID, sessionID, now)
	if err != nil {
		return fmt.Errorf("tokens: consume rebind commands: %w", err)
	}
	return nil
}

// ConsumeGCCommands marks gc {workdir_ids} commands consumed once the
// runtime's workdir report (§6) lists none of their workdirs any more.
func ConsumeGCCommands(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, presentWorkdirIDs []string, now time.Time) error {
	if presentWorkdirIDs == nil {
		presentWorkdirIDs = []string{}
	}
	_, err := q.Exec(ctx, `
		UPDATE daemon_command SET consumed_at = $3, consumed_by = 'workdir_report'
		WHERE runtime_id = $1 AND type = 'gc' AND consumed_at IS NULL
		  AND jsonb_typeof(payload->'workdir_ids') = 'array'
		  AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(payload->'workdir_ids') w WHERE w = ANY($2))`,
		runtimeID, presentWorkdirIDs, now)
	if err != nil {
		return fmt.Errorf("tokens: consume gc commands: %w", err)
	}
	return nil
}

// ExpiredCommand is a command dropped by ExpireCommands for the 24h TTL.
type ExpiredCommand struct {
	ID      int64
	Type    contracts.CommandType
	TaskID  *uuid.UUID
	Attempt *int
}

// ExpireCommands applies the two time bounds of §4.3 to the stored rows:
// revoke older than HeartbeatExpiry is consumed silently (the orphan is then
// stopped by §5 restart cleanup and 401), anything older than CommandTTL is
// consumed as 'ttl' and returned so the caller can record it on the feed.
func ExpireCommands(ctx context.Context, q db.DBTX, now time.Time) ([]ExpiredCommand, error) {
	if _, err := q.Exec(ctx, `
		UPDATE daemon_command SET consumed_at = $1, consumed_by = 'revoke_expiry'
		WHERE type = 'revoke' AND consumed_at IS NULL AND created_at <= $2`, now, now.Add(-contracts.HeartbeatExpiry)); err != nil {
		return nil, fmt.Errorf("tokens: expire revoke: %w", err)
	}
	rows, err := q.Query(ctx, `
		UPDATE daemon_command SET consumed_at = $1, consumed_by = 'ttl'
		WHERE consumed_at IS NULL AND created_at <= $2
		RETURNING id, type, task_id, attempt`, now, now.Add(-CommandTTL))
	if err != nil {
		return nil, fmt.Errorf("tokens: expire commands: %w", err)
	}
	defer rows.Close()
	var out []ExpiredCommand
	for rows.Next() {
		var e ExpiredCommand
		var typ string
		if err := rows.Scan(&e.ID, &typ, &e.TaskID, &e.Attempt); err != nil {
			return nil, err
		}
		e.Type = contracts.CommandType(typ)
		out = append(out, e)
	}
	return out, rows.Err()
}
