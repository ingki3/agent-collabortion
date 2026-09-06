// Package artifacts is FR-4.3: the session's artifact store. Submitting the
// same name again is a new version, never an overwrite — an artifact is the
// only thing an agent in another lane may read (FR-6.1), so losing the bytes
// someone else already cited is worse than keeping both.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// MaxBytes is the contract's upload ceiling (openapi submitArtifact: 50 MB →
// 413). The CLI checks the same number before it sends, so a request that
// arrives over the line is either a non-CLI caller or a lie about the size.
const MaxBytes = 50 << 20

// storagePrefix marks how the body is stored. The value says which backend
// wrote it, so a later move to object storage can migrate row by row instead
// of guessing from the shape of the string.
const storagePrefix = "pglo:"

type Service struct {
	DB    *pgxpool.Pool
	Clock clock.Clock
}

func New(pool *pgxpool.Pool, c clock.Clock) *Service { return &Service{DB: pool, Clock: c} }

// Row is one artifact row plus the review that settled it, if any.
type Row struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	Name       string
	Version    int
	Type       string
	StorageRef string
	SizeBytes  int64

	ContentType *string
	Description *string

	SubmittedByTaskID  *uuid.UUID
	SubmittedByAgentID *uuid.UUID
	SubmittedByUserID  *uuid.UUID
	AgentName          *string

	Latest    bool
	CreatedAt time.Time

	Review *ReviewRow
}

// ReviewRow is the artifact_review row (FR-2.2 agent_approval).
type ReviewRow struct {
	ArtifactID      uuid.UUID
	Verdict         string
	Comments        *string
	ReviewerAgentID uuid.UUID
	ReviewerTaskID  *uuid.UUID
	DecisionID      *uuid.UUID
	ReviewedAt      time.Time
}

// SubmitInput is one submitArtifact call. Content is the `file` part already
// bounded by MaxBytes; the handler is what turns an oversized body into 413,
// because the limit is an HTTP status, not a storage property.
type SubmitInput struct {
	Name        string
	Type        string
	Description string
	ContentType string
	Content     []byte

	TaskID  *uuid.UUID
	AgentID *uuid.UUID
	UserID  *uuid.UUID
}

// Submit stores the bytes and the row. The version is assigned under the
// session row lock: two agents submitting `report.md` at the same moment must
// get v1 and v2, and `max(version)+1` read outside a lock hands both v1 and
// then fails one on the unique index.
func (s *Service) Submit(ctx context.Context, sessionID uuid.UUID, in SubmitInput) (*Row, error) {
	if int64(len(in.Content)) > MaxBytes {
		return nil, apperr.New(413, "payload_too_large",
			fmt.Sprintf("artifact body is %d bytes; the limit is %d (50 MB)", len(in.Content), MaxBytes))
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status string
	err = tx.QueryRow(ctx, `SELECT status::text FROM session WHERE id = $1 FOR UPDATE`, sessionID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, err
	}
	if status == "completed" || status == "cancelled" {
		return nil, apperr.Conflict("session_closed", "this session is already closed")
	}

	// The large object is created inside this transaction, so a failed insert
	// below takes the bytes with it rather than leaking an orphan blob.
	lo := tx.LargeObjects()
	oid, err := lo.Create(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("artifacts: create blob: %w", err)
	}
	obj, err := lo.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return nil, fmt.Errorf("artifacts: open blob: %w", err)
	}
	if _, err := obj.Write(in.Content); err != nil {
		return nil, fmt.Errorf("artifacts: write blob: %w", err)
	}
	if err := obj.Close(); err != nil {
		return nil, fmt.Errorf("artifacts: close blob: %w", err)
	}

	var ct, desc *string
	if in.ContentType != "" {
		ct = &in.ContentType
	}
	if in.Description != "" {
		desc = &in.Description
	}
	var out Row
	err = tx.QueryRow(ctx, `
		INSERT INTO artifact (session_id, name, version, type, storage_ref, size_bytes, content_type,
		                      description, submitted_by_task_id, submitted_by_agent_id, submitted_by_user_id, created_at)
		VALUES ($1, $2, (SELECT coalesce(max(version), 0) + 1 FROM artifact WHERE session_id = $1 AND name = $2),
		        $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, version, created_at`,
		sessionID, in.Name, in.Type, storagePrefix+fmt.Sprint(oid), int64(len(in.Content)), ct, desc,
		in.TaskID, in.AgentID, in.UserID, now).
		Scan(&out.ID, &out.Version, &out.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("artifacts: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.SessionID, out.Name, out.Type = sessionID, in.Name, in.Type
	out.StorageRef, out.SizeBytes, out.ContentType, out.Description = storagePrefix+fmt.Sprint(oid), int64(len(in.Content)), ct, desc
	out.SubmittedByTaskID, out.SubmittedByAgentID, out.SubmittedByUserID = in.TaskID, in.AgentID, in.UserID
	out.Latest = true
	return &out, nil
}

// selectSQL is the read model: the row, its agent's display name, whether it
// is the newest version of its name, and the review if one landed.
const selectSQL = `
	SELECT a.id, a.session_id, a.name, a.version, a.type, a.storage_ref, a.size_bytes, a.content_type,
	       a.description, a.submitted_by_task_id, a.submitted_by_agent_id, a.submitted_by_user_id,
	       ag.name, a.created_at,
	       a.version = (SELECT max(b.version) FROM artifact b WHERE b.session_id = a.session_id AND b.name = a.name),
	       r.verdict::text, r.comments, r.reviewer_agent_id, r.reviewer_task_id, r.decision_id, r.reviewed_at
	FROM artifact a
	LEFT JOIN agent ag ON ag.id = a.submitted_by_agent_id
	LEFT JOIN artifact_review r ON r.artifact_id = a.id`

func scan(row pgx.Row) (*Row, error) {
	var a Row
	var verdict *string
	var rev ReviewRow
	var reviewer *uuid.UUID
	var reviewedAt *time.Time
	if err := row.Scan(&a.ID, &a.SessionID, &a.Name, &a.Version, &a.Type, &a.StorageRef, &a.SizeBytes, &a.ContentType,
		&a.Description, &a.SubmittedByTaskID, &a.SubmittedByAgentID, &a.SubmittedByUserID,
		&a.AgentName, &a.CreatedAt, &a.Latest,
		&verdict, &rev.Comments, &reviewer, &rev.ReviewerTaskID, &rev.DecisionID, &reviewedAt); err != nil {
		return nil, err
	}
	if verdict != nil && reviewer != nil && reviewedAt != nil {
		rev.ArtifactID, rev.Verdict, rev.ReviewerAgentID, rev.ReviewedAt = a.ID, *verdict, *reviewer, *reviewedAt
		a.Review = &rev
	}
	return &a, nil
}

// Get loads one artifact. The workspace boundary is enforced by the caller
// (the session it belongs to), which is why this returns the session id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Row, error) {
	a, err := scan(s.DB.QueryRow(ctx, selectSQL+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("artifact")
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListOptions mirrors listArtifacts' query parameters.
type ListOptions struct {
	LatestOnly bool
	Type       string
}

// List is the sidebar's read model. The order is submission order because
// that is also the order a rebound session re-applies them in (E14-06).
func (s *Service) List(ctx context.Context, sessionID uuid.UUID, o ListOptions) ([]*Row, error) {
	q := selectSQL + ` WHERE a.session_id = $1`
	args := []any{sessionID}
	if o.Type != "" {
		args = append(args, o.Type)
		q += fmt.Sprintf(` AND a.type = $%d`, len(args))
	}
	if o.LatestOnly {
		q += ` AND a.version = (SELECT max(b.version) FROM artifact b WHERE b.session_id = a.session_id AND b.name = a.name)`
	}
	q += ` ORDER BY a.created_at, a.version`
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Row{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Content is an open read of the artifact body. The caller MUST Close it: a
// large object lives inside a transaction, so the transaction stays open until
// the last byte is written to the response.
type Content struct {
	Size        int64
	ContentType string
	Name        string

	r  io.Reader
	tx pgx.Tx
}

func (c *Content) Read(p []byte) (int, error) { return c.r.Read(p) }

// Close ends the transaction the blob is read inside. It is always a
// rollback: nothing was written.
func (c *Content) Close() error { return c.tx.Rollback(context.Background()) }

// Open returns the body as a stream. Nothing here reads the bytes: a 50 MB
// artifact must not sit in the server's heap while a slow client drains it
// (openapi downloadArtifact, and the CLI checks the byte count against the
// declared Content-Length).
func (s *Service) Open(ctx context.Context, a *Row) (*Content, error) {
	var oid uint32
	if _, err := fmt.Sscanf(a.StorageRef, storagePrefix+"%d", &oid); err != nil {
		return nil, fmt.Errorf("artifacts: unreadable storage_ref %q: %w", a.StorageRef, err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	lo := tx.LargeObjects()
	obj, err := lo.Open(ctx, oid, pgx.LargeObjectModeRead)
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("artifacts: open blob: %w", err)
	}
	c := &Content{Size: a.SizeBytes, Name: a.Name, r: obj, tx: tx}
	if a.ContentType != nil {
		c.ContentType = *a.ContentType
	}
	if c.ContentType == "" {
		c.ContentType = "application/octet-stream"
	}
	return c, nil
}

// RecordReview stores the verdict. `reject` does not remove or supersede the
// artifact: it stays readable at its version and the reason travels back on
// the submitting lane's thread (openapi reviewArtifact).
func (s *Service) RecordReview(ctx context.Context, artifactID uuid.UUID, rev ReviewRow) (*ReviewRow, error) {
	now := s.Clock.Now()
	rev.ArtifactID, rev.ReviewedAt = artifactID, now
	_, err := s.DB.Exec(ctx, `
		INSERT INTO artifact_review (artifact_id, verdict, comments, reviewer_agent_id, reviewer_task_id, decision_id, reviewed_at)
		VALUES ($1, $2::review_verdict, $3, $4, $5, $6, $7)
		ON CONFLICT (artifact_id) DO UPDATE SET
			verdict = EXCLUDED.verdict, comments = EXCLUDED.comments,
			reviewer_agent_id = EXCLUDED.reviewer_agent_id, reviewer_task_id = EXCLUDED.reviewer_task_id,
			decision_id = EXCLUDED.decision_id, reviewed_at = EXCLUDED.reviewed_at`,
		artifactID, rev.Verdict, rev.Comments, rev.ReviewerAgentID, rev.ReviewerTaskID, rev.DecisionID, now)
	if err != nil {
		return nil, fmt.Errorf("artifacts: record review: %w", err)
	}
	return &rev, nil
}
