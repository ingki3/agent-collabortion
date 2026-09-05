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

// QueueCommand appends a server → daemon command for runtimeID. It is handed
// out by the next claim / events / heartbeat response (daemon-protocol §4.3).
func QueueCommand(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, cmd contracts.Command) error {
	if _, err := q.Exec(ctx, `INSERT INTO daemon_command (runtime_id, type, payload) VALUES ($1, $2, $3)`,
		runtimeID, string(cmd.Type), cmd); err != nil {
		return fmt.Errorf("tokens: queue command: %w", err)
	}
	return nil
}

// PendingCommands returns and marks delivered every undelivered command of the
// runtime. Delivery is at-least-once: the daemon dedupes on (type, task, attempt).
func PendingCommands(ctx context.Context, q db.DBTX, runtimeID uuid.UUID) ([]contracts.Command, error) {
	rows, err := q.Query(ctx, `
		UPDATE daemon_command SET delivered_at = now()
		WHERE runtime_id = $1 AND delivered_at IS NULL
		RETURNING payload`, runtimeID)
	if err != nil {
		return nil, fmt.Errorf("tokens: pending commands: %w", err)
	}
	defer rows.Close()
	out := []contracts.Command{}
	for rows.Next() {
		var c contracts.Command
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
