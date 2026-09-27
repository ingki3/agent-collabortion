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
	"path/filepath"
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
	// ContentTypeJudged says ContentType is the server's own judgment
	// (openapi v0.3.7, migration 0040). A row stored before that carries
	// what the uploader claimed until judgeLegacy reads its first bytes.
	ContentTypeJudged bool
	Description       *string

	SubmittedByTaskID  *uuid.UUID
	SubmittedByAgentID *uuid.UUID
	SubmittedByUserID  *uuid.UUID
	AgentName          *string

	Latest    bool
	CreatedAt time.Time

	// WorkID is the mission the artifact belongs to (PRD v0.19 FR-2A.5 — the
	// submitting task's; nil for a run outside any mission). Its completion
	// condition is the one the submission counts toward.
	WorkID *uuid.UUID

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
	// ContentType is what the uploader's `file` part declared. It is kept
	// only for the idempotency hash: the stored content_type is the
	// server's judgment (DetectContentType), never this (FR-4.3.1 rule 1).
	ContentType string
	// FileName is the `file` part's filename; its extension is what
	// DetectContentType reads when the bytes have no signature. Empty falls
	// back to Name.
	FileName string
	Content  []byte

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
			fmt.Sprintf("파일이 너무 큽니다 — %d바이트를 받았고 상한은 %d바이트(50 MB)입니다", len(in.Content), MaxBytes))
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The room is the lock (versions are per room × name, FR-2A.5 [V19-B]);
	// the mission is the submitting task's (FR-3.1.1: a task runs for its
	// lane's mission), else — a person submitting with no task — the room's
	// legacy mission. A closed mission takes no
	// more artifacts.
	var legacy *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT legacy_work_id FROM room WHERE id = $1 FOR UPDATE`, sessionID).Scan(&legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, err
	}
	work := legacy
	if in.TaskID != nil {
		work = nil
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(l.work_id, t.work_id) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, *in.TaskID).Scan(&work); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	if work != nil {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status::text FROM work WHERE id = $1 FOR UPDATE`, *work).Scan(&status); err != nil {
			return nil, err
		}
		if status == "completed" || status == "cancelled" {
			if legacy != nil && *legacy == *work {
				return nil, apperr.Conflict("session_closed", "이미 끝난 미션입니다")
			}
			return nil, apperr.Conflict("work_closed", "이미 끝난 미션입니다")
		}
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

	judged := DetectContentType(judgeName(in.FileName, in.Name), in.Content)
	ct := &judged
	var desc *string
	if in.Description != "" {
		desc = &in.Description
	}
	var out Row
	err = tx.QueryRow(ctx, `
		INSERT INTO artifact (session_id, name, version, type, storage_ref, size_bytes, content_type,
		                      description, submitted_by_task_id, submitted_by_agent_id, submitted_by_user_id, created_at, work_id,
		                      content_type_judged)
		VALUES ($1, $2, (SELECT coalesce(max(version), 0) + 1 FROM artifact WHERE session_id = $1 AND name = $2),
		        $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, true)
		RETURNING id, version, created_at`,
		sessionID, in.Name, in.Type, storagePrefix+fmt.Sprint(oid), int64(len(in.Content)), ct, desc,
		in.TaskID, in.AgentID, in.UserID, now, work).
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
	out.WorkID = work
	out.ContentTypeJudged = true
	return &out, nil
}

// judgeName is the name whose extension DetectContentType reads: the
// uploaded file's own name when it has an extension, else the artifact name.
func judgeName(fileName, name string) string {
	if filepath.Ext(fileName) != "" {
		return fileName
	}
	return name
}

// selectSQL is the read model: the row, its agent's display name, whether it
// is the newest version of its name, and the LATEST review if one landed
// (artifact_review_latest, migration 0024 — the table itself is the history).
const selectSQL = `
	SELECT a.id, a.session_id, a.name, a.version, a.type, a.storage_ref, a.size_bytes, a.content_type,
	       a.description, a.submitted_by_task_id, a.submitted_by_agent_id, a.submitted_by_user_id,
	       ag.name, a.created_at,
	       a.version = (SELECT max(b.version) FROM artifact b WHERE b.session_id = a.session_id AND b.name = a.name),
	       r.verdict::text, r.comments, r.reviewer_agent_id, r.reviewer_task_id, r.decision_id, r.reviewed_at,
	       a.work_id, a.content_type_judged
	FROM artifact a
	LEFT JOIN agent ag ON ag.id = a.submitted_by_agent_id
	LEFT JOIN artifact_review_latest r ON r.artifact_id = a.id`

func scan(row pgx.Row) (*Row, error) {
	var a Row
	var verdict *string
	var rev ReviewRow
	var reviewer *uuid.UUID
	var reviewedAt *time.Time
	if err := row.Scan(&a.ID, &a.SessionID, &a.Name, &a.Version, &a.Type, &a.StorageRef, &a.SizeBytes, &a.ContentType,
		&a.Description, &a.SubmittedByTaskID, &a.SubmittedByAgentID, &a.SubmittedByUserID,
		&a.AgentName, &a.CreatedAt, &a.Latest,
		&verdict, &rev.Comments, &reviewer, &rev.ReviewerTaskID, &rev.DecisionID, &reviewedAt, &a.WorkID, &a.ContentTypeJudged); err != nil {
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
	if err := s.judgeLegacy(ctx, a); err != nil {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, a := range out {
		if err := s.judgeLegacy(ctx, a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Content is an open read of the artifact body. The caller MUST Close it: a
// large object lives inside a transaction, so the transaction stays open until
// the last byte is written to the response.
type Content struct {
	Size        int64
	ContentType string
	Name        string

	r  io.ReadSeeker
	tx pgx.Tx
}

func (c *Content) Read(p []byte) (int, error) { return c.r.Read(p) }

// Seek moves inside the body — downloadArtifact's `Range` (openapi v0.3.7)
// starts a partial response at an offset without reading what precedes it.
func (c *Content) Seek(offset int64, whence int) (int64, error) { return c.r.Seek(offset, whence) }

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

// RecordReview stores the verdict as a NEW row (S-15, migration 0024): a
// re-review does not overwrite the previous judgment — the table is the
// history, and the artifact's `review` is the latest row of it. `reject`
// does not remove or supersede the artifact: it stays readable at its
// version and the reason travels back on the submitting lane's thread
// (openapi reviewArtifact).
func (s *Service) RecordReview(ctx context.Context, artifactID uuid.UUID, rev ReviewRow) (*ReviewRow, error) {
	now := s.Clock.Now()
	rev.ArtifactID, rev.ReviewedAt = artifactID, now
	_, err := s.DB.Exec(ctx, `
		INSERT INTO artifact_review (artifact_id, verdict, comments, reviewer_agent_id, reviewer_task_id, decision_id, reviewed_at)
		VALUES ($1, $2::review_verdict, $3, $4, $5, $6, $7)`,
		artifactID, rev.Verdict, rev.Comments, rev.ReviewerAgentID, rev.ReviewerTaskID, rev.DecisionID, now)
	if err != nil {
		return nil, fmt.Errorf("artifacts: record review: %w", err)
	}
	return &rev, nil
}

// ReviewHistory is every judgment recorded for the artifact, newest first
// (S-15). Nothing on the API reads it yet — the review UI's "who reversed
// what, when" is the consumer; until then it is what the tests hold the
// append-only rule to.
func (s *Service) ReviewHistory(ctx context.Context, artifactID uuid.UUID) ([]ReviewRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT artifact_id, verdict::text, comments, reviewer_agent_id, reviewer_task_id, decision_id, reviewed_at
		FROM artifact_review WHERE artifact_id = $1 ORDER BY reviewed_at DESC, id DESC`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: review history: %w", err)
	}
	defer rows.Close()
	var out []ReviewRow
	for rows.Next() {
		var r ReviewRow
		if err := rows.Scan(&r.ArtifactID, &r.Verdict, &r.Comments, &r.ReviewerAgentID, &r.ReviewerTaskID, &r.DecisionID, &r.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// judgeLegacy is migration 0040's read-time judgment for a row stored before
// the server judged content types: read at most 512 bytes of the body, judge
// them with the name (no filename was kept — the name is what there is),
// store the answer and mark the row, once. The UPDATE is conditional on the
// mark, so two readers racing on one row write the same answer at most twice
// and never undo each other.
//
// Why at read time rather than in the migration: the judgment is Go's table
// (http.DetectContentType + the extension rules); rewriting it in SQL over
// large objects would be a second copy of the rule that drifts.
func (s *Service) judgeLegacy(ctx context.Context, a *Row) error {
	if a.ContentTypeJudged {
		return nil
	}
	head, err := s.readHead(ctx, a, 512)
	if err != nil {
		return err
	}
	judged := DetectContentType(a.Name, head)
	if _, err := s.DB.Exec(ctx, `UPDATE artifact SET content_type = $2, content_type_judged = true
		WHERE id = $1 AND NOT content_type_judged`, a.ID, judged); err != nil {
		return fmt.Errorf("artifacts: judge legacy content type: %w", err)
	}
	a.ContentType, a.ContentTypeJudged = &judged, true
	return nil
}

// readHead reads the first n bytes of the body (fewer when it is shorter).
func (s *Service) readHead(ctx context.Context, a *Row, n int) ([]byte, error) {
	c, err := s.Open(ctx, a)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	buf := make([]byte, n)
	k, err := io.ReadFull(c, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("artifacts: read head: %w", err)
	}
	return buf[:k], nil
}
