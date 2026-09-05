// Package runtimes is FR-9: pairing codes (S12) exchanged by the daemon for a
// runtime + daemon token (daemon-protocol §2), probe storage (§3), presence
// and the runtime list/detail.
package runtimes

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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

const (
	PairingTTL        = 30 * time.Minute // openapi createPairing default
	DaemonTokenPrefix = "cdt_"
	PairingCodePrefix = "cpc_"
)

var ErrInvalidDaemonToken = errors.New("runtimes: invalid daemon token")

type Service struct {
	DB        *pgxpool.Pool
	Clock     clock.Clock
	Hub       *realtime.Hub
	ServerURL string // shown in install_commands
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, serverURL string) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, ServerURL: serverURL}
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func random(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// CreatePairing issues a one-time pairing code (S12).
func (s *Service) CreatePairing(ctx context.Context, wsID, userID uuid.UUID, name *string) (*gen.Pairing, error) {
	code := random(PairingCodePrefix, 24)
	now := s.Clock.Now()
	var id uuid.UUID
	if err := s.DB.QueryRow(ctx, `
		INSERT INTO runtime_pairing (workspace_id, name, code_hash, created_by, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, wsID, name, hash(code), userID, now.Add(PairingTTL), now).Scan(&id); err != nil {
		return nil, fmt.Errorf("runtimes: create pairing: %w", err)
	}
	p, err := s.GetPairing(ctx, wsID, id)
	if err != nil {
		return nil, err
	}
	p.PairingToken = &code
	p.InstallCommands = s.installCommands(code)
	return p, nil
}

func (s *Service) installCommands(code string) []string {
	return []string{
		fmt.Sprintf("curl -fsSL %s/install.sh | sh", s.ServerURL),
		fmt.Sprintf("colab-daemon pair %s --server %s", code, s.ServerURL),
	}
}

// GetPairing returns the S12 stage. Expired → 410 pairing_expired.
func (s *Service) GetPairing(ctx context.Context, wsID, id uuid.UUID) (*gen.Pairing, error) {
	var p gen.Pairing
	var status string
	var runtimeID *uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT id, status, runtime_id, expires_at, created_at FROM runtime_pairing WHERE id = $1 AND workspace_id = $2`, id, wsID).
		Scan(&p.Id, &status, &runtimeID, &p.ExpiresAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("pairing")
	}
	if err != nil {
		return nil, err
	}
	if status == "waiting" && !s.Clock.Now().Before(p.ExpiresAt) {
		status = "expired"
	}
	if status == "expired" {
		return nil, apperr.Gone("pairing_expired", "this pairing code has expired; create a new one")
	}
	p.Status = gen.PairingStatus(status)
	p.InstallCommands = s.installCommands("<pairing_token>")
	if runtimeID != nil {
		if rt, err := s.Get(ctx, *runtimeID); err == nil {
			p.Runtime = &rt.Runtime
		}
	}
	return &p, nil
}

// Pair is POST /v1/daemon/pair: code → runtime + daemon token (§2).
func (s *Service) Pair(ctx context.Context, code, hostname, os, daemonVersion string) (runtimeID uuid.UUID, daemonToken string, err error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var pid, wsID uuid.UUID
	var status string
	var name *string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT id, workspace_id, status, name, expires_at FROM runtime_pairing WHERE code_hash = $1 FOR UPDATE`, hash(code)).
		Scan(&pid, &wsID, &status, &name, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", apperr.Unauthorized("pairing_invalid", "unknown pairing code")
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	if status != "waiting" {
		return uuid.Nil, "", apperr.Gone("pairing_used", "this pairing code was already used")
	}
	if !now.Before(expires) {
		_, _ = tx.Exec(ctx, `UPDATE runtime_pairing SET status = 'expired' WHERE id = $1`, pid)
		_ = tx.Commit(ctx)
		return uuid.Nil, "", apperr.Gone("pairing_expired", "this pairing code has expired")
	}
	rtName := hostname
	if rtName == "" {
		if name != nil {
			rtName = *name
		} else {
			rtName = "runtime"
		}
	}
	daemonToken = random(DaemonTokenPrefix, 32)
	host := hostname
	for i := 0; ; i++ {
		candidate := rtName
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", rtName, i+1)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO runtime (workspace_id, name, host, status, daemon_version, last_seen_at, daemon_token_hash, created_at, updated_at)
			VALUES ($1, $2, $3, 'online', $4, $5, $6, $5, $5) RETURNING id`,
			wsID, candidate, host, daemonVersion, now, hash(daemonToken)).Scan(&runtimeID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && i < 20 {
			continue
		}
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("runtimes: insert runtime: %w", err)
		}
		break
	}
	_ = os
	if _, err := tx.Exec(ctx, `UPDATE runtime_pairing SET status = 'connected', runtime_id = $2, connected_at = $3 WHERE id = $1`, pid, runtimeID, now); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}
	s.publishPairing(ctx, wsID, pid)
	s.publishRuntime(ctx, runtimeID)
	return runtimeID, daemonToken, nil
}

// VerifyDaemonToken resolves a `cdt_` bearer to (runtime, workspace).
func (s *Service) VerifyDaemonToken(ctx context.Context, token string) (runtimeID, wsID uuid.UUID, err error) {
	if !strings.HasPrefix(token, DaemonTokenPrefix) {
		return uuid.Nil, uuid.Nil, ErrInvalidDaemonToken
	}
	err = s.DB.QueryRow(ctx, `SELECT id, workspace_id FROM runtime WHERE daemon_token_hash = $1`, hash(token)).Scan(&runtimeID, &wsID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrInvalidDaemonToken
	}
	return runtimeID, wsID, err
}

// Touch records presence on every daemon call (long-poll included).
func (s *Service) Touch(ctx context.Context, runtimeID uuid.UUID) {
	now := s.Clock.Now()
	tag, err := s.DB.Exec(ctx, `UPDATE runtime SET last_seen_at = $2, status = 'online', offline_since = NULL, updated_at = CASE WHEN status = 'offline' THEN $2 ELSE updated_at END WHERE id = $1 AND (status = 'offline' OR last_seen_at IS NULL OR last_seen_at < $3)`,
		runtimeID, now, now.Add(-10*time.Second))
	if err == nil && tag.RowsAffected() > 0 {
		var wasOffline bool
		_ = s.DB.QueryRow(ctx, `SELECT updated_at = $2 FROM runtime WHERE id = $1`, runtimeID, now).Scan(&wasOffline)
		if wasOffline {
			s.publishRuntime(ctx, runtimeID)
		}
	}
}

// Probe stores the daemon's capability report (§3) and completes S12
// (pairing → ready, E11-08).
func (s *Service) Probe(ctx context.Context, runtimeID uuid.UUID, p contracts.Probe) error {
	now := s.Clock.Now()
	caps := p.Capabilities
	if caps == nil {
		caps = []contracts.Capability{}
	}
	repos := p.Repos
	if repos == nil {
		repos = []contracts.Repo{}
	}
	if _, err := s.DB.Exec(ctx, `
		UPDATE runtime SET capabilities = $2, repos = $3, daemon_version = COALESCE(NULLIF($4, ''), daemon_version),
		       host = COALESCE(NULLIF($5, ''), host), last_seen_at = $6, status = 'online', offline_since = NULL, updated_at = $6
		WHERE id = $1`, runtimeID, caps, repos, p.DaemonVersion, p.Hostname, now); err != nil {
		return fmt.Errorf("runtimes: probe: %w", err)
	}
	var pid, wsID uuid.UUID
	if err := s.DB.QueryRow(ctx, `UPDATE runtime_pairing SET status = 'ready', ready_at = $2 WHERE runtime_id = $1 AND status IN ('connected', 'probing') RETURNING id, workspace_id`, runtimeID, now).Scan(&pid, &wsID); err == nil {
		s.publishPairing(ctx, wsID, pid)
	}
	s.publishRuntime(ctx, runtimeID)
	return nil
}

func (s *Service) publishRuntime(ctx context.Context, id uuid.UUID) {
	if s.Hub == nil {
		return
	}
	if rt, err := s.Get(ctx, id); err == nil {
		_ = s.Hub.Publish(ctx, nil, rt.WorkspaceId, nil, "runtime.updated", rt.Runtime)
	}
}

func (s *Service) publishPairing(ctx context.Context, wsID, id uuid.UUID) {
	if s.Hub == nil {
		return
	}
	if p, err := s.GetPairing(ctx, wsID, id); err == nil {
		_ = s.Hub.Publish(ctx, nil, wsID, nil, "pairing.updated", p)
	}
}

// List returns the workspace's runtimes (S11).
func (s *Service) List(ctx context.Context, wsID uuid.UUID, status *string) ([]gen.Runtime, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM runtime WHERE workspace_id = $1 AND ($2::text IS NULL OR status::text = $2) ORDER BY created_at`, wsID, status)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []gen.Runtime{}
	for _, id := range ids {
		d, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d.Runtime)
	}
	return out, nil
}

// Get returns RuntimeDetail (runtime + active sessions bound to it).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Detail, error) {
	return Load(ctx, s.DB, id)
}

func Load(ctx context.Context, q db.DBTX, id uuid.UUID) (*Detail, error) {
	var r gen.Runtime
	var status string
	var host, version *string
	var lastSeen, offlineSince *time.Time
	var grace time.Duration
	var running, paused int
	var disk int64
	err := q.QueryRow(ctx, `
		SELECT r.id, r.workspace_id, r.name, r.host, r.status, r.daemon_version, r.last_seen_at, r.capabilities, r.repos, r.offline_since,
		       r.created_at, r.updated_at,
		       COALESCE((SELECT ws.runtime_offline_grace FROM workspace_settings ws WHERE ws.workspace_id = r.workspace_id), interval '7 days'),
		       (SELECT count(*) FROM task t WHERE t.runtime_id = r.id AND t.status IN ('dispatched','preparing','running')),
		       (SELECT count(*) FROM session s WHERE s.runtime_id = r.id AND s.status = 'paused' AND s.paused_reason = 'runtime_offline'),
		       COALESCE((SELECT sum(w.disk_bytes) FROM workdir w JOIN session s ON s.id = w.session_id WHERE s.runtime_id = r.id AND w.status <> 'deleted'), 0)
		FROM runtime r WHERE r.id = $1`, id).Scan(
		&r.Id, &r.WorkspaceId, &r.Name, &host, &status, &version, &lastSeen, &r.Capabilities, &r.Repos, &offlineSince,
		&r.CreatedAt, &r.UpdatedAt, &grace, &running, &paused, &disk)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("runtime")
	}
	if err != nil {
		return nil, fmt.Errorf("runtimes: load: %w", err)
	}
	r.Status = gen.RuntimeStatus(status)
	r.Host = tasks.NullString(host)
	r.DaemonVersion = tasks.NullString(version)
	r.LastSeenAt = tasks.NullTime(lastSeen)
	r.OfflineSince = tasks.NullTime(offlineSince)
	r.GraceEndsAt = nullable.NewNullNullable[time.Time]()
	if offlineSince != nil {
		r.GraceEndsAt = nullable.NewNullableWithValue(offlineSince.Add(grace))
	}
	r.MaxConcurrentTasks = nullable.NewNullNullable[int]()
	r.RunningTaskCount = running
	r.PausedSessionCount = &paused
	r.WorkdirDiskBytes = &disk
	if r.Capabilities == nil {
		r.Capabilities = []gen.RuntimeCapability{}
	}
	if r.Repos == nil {
		r.Repos = []gen.RuntimeRepo{}
	}
	d := &Detail{Runtime: r, ActiveSessions: []gen.SessionRef{}}
	rows, err := q.Query(ctx, `SELECT id, title, status FROM session WHERE runtime_id = $1 AND status IN ('active','paused','completing') ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref gen.SessionRef
		var st string
		if err := rows.Scan(&ref.Id, &ref.Title, &st); err != nil {
			return nil, err
		}
		ref.Status = gen.SessionStatus(st)
		d.ActiveSessions = append(d.ActiveSessions, ref)
	}
	return d, rows.Err()
}

// Count returns how many runtimes a workspace has (createSession 409 no_runtime).
func (s *Service) Count(ctx context.Context, wsID uuid.UUID) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM runtime WHERE workspace_id = $1`, wsID).Scan(&n)
	return n, err
}

// Detail is RuntimeDetail (openapi allOf Runtime + active_sessions); the
// embedded Runtime marshals flat.
type Detail struct {
	gen.Runtime
	ActiveSessions []gen.SessionRef `json:"active_sessions"`
}
