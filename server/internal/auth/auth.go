// Package auth is S1/S2/S3: email + password accounts (argon2id), user
// sessions (cookie `colab_session` or `Authorization: Bearer cus_…`),
// workspaces, membership and invite links.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

const (
	SessionPrefix = "cus_"
	SessionTTL    = 30 * 24 * time.Hour
	CookieName    = "colab_session"
)

var ErrNoSession = errors.New("auth: no valid user session")

type Service struct {
	DB      *pgxpool.Pool
	Clock   clock.Clock
	BaseURL string // web origin for invite URLs (COLAB_WEB_URL, else the server URL)
}

func New(pool *pgxpool.Pool, c clock.Clock, baseURL string) *Service {
	return &Service{DB: pool, Clock: c, BaseURL: baseURL}
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

// randomHex is the slug-safe tail for a crowded slug stem ([a-z0-9] only).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Signup creates the account, a user session and (optionally) accepts an invite.
func (s *Service) Signup(ctx context.Context, displayName, email, password string, inviteToken *string) (*gen.AuthResult, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var errs []apperr.FieldError
	if strings.TrimSpace(displayName) == "" || len(displayName) > 80 {
		errs = append(errs, apperr.Field("display_name", "length", "display_name must be 1–80 characters"))
	}
	if !emailRe.MatchString(email) {
		errs = append(errs, apperr.Field("email", "format", "invalid email"))
	}
	if len(password) < 8 {
		errs = append(errs, apperr.Field("password", "min_length", "password must be at least 8 characters"))
	}
	if len(errs) > 0 {
		return nil, "", apperr.Validation(errs...)
	}
	ph, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}
	var userID uuid.UUID
	err = s.DB.QueryRow(ctx, `INSERT INTO app_user (email, display_name, password_hash, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		email, strings.TrimSpace(displayName), ph, s.Clock.Now()).Scan(&userID)
	if isUnique(err) {
		return nil, "", apperr.Conflict("email_taken", "an account with this email already exists")
	}
	if err != nil {
		return nil, "", fmt.Errorf("auth: signup: %w", err)
	}
	return s.finishAuth(ctx, userID, inviteToken)
}

// Login checks the password; failures use Problem.code account_not_found /
// password_mismatch (S1).
func (s *Service) Login(ctx context.Context, email, password string, inviteToken *string) (*gen.AuthResult, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var userID uuid.UUID
	var ph *string
	err := s.DB.QueryRow(ctx, `SELECT id, password_hash FROM app_user WHERE email = $1`, email).Scan(&userID, &ph)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", apperr.Unauthorized("account_not_found", "no account with this email")
	}
	if err != nil {
		return nil, "", err
	}
	if ph == nil {
		return nil, "", apperr.Unauthorized("password_mismatch", "this account has no password login")
	}
	ok, err := VerifyPassword(*ph, password)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", apperr.Unauthorized("password_mismatch", "wrong password")
	}
	return s.finishAuth(ctx, userID, inviteToken)
}

func (s *Service) finishAuth(ctx context.Context, userID uuid.UUID, inviteToken *string) (*gen.AuthResult, string, error) {
	token := random(SessionPrefix, 32)
	now := s.Clock.Now()
	if _, err := s.DB.Exec(ctx, `INSERT INTO user_session (user_id, token_hash, created_at, expires_at, last_seen_at) VALUES ($1, $2, $3, $4, $3)`,
		userID, hash(token), now, now.Add(SessionTTL)); err != nil {
		return nil, "", fmt.Errorf("auth: session: %w", err)
	}
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	res := &gen.AuthResult{User: *u}
	if inviteToken != nil && *inviteToken != "" {
		m, err := s.AcceptInvite(ctx, *inviteToken, userID)
		if err != nil {
			return nil, "", err
		}
		res.AcceptedInvite = m
	}
	return res, token, nil
}

// Logout revokes the session token.
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.DB.Exec(ctx, `UPDATE user_session SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, hash(token), s.Clock.Now())
	return err
}

// Resolve maps a session token to the user (ErrNoSession if invalid).
func (s *Service) Resolve(ctx context.Context, token string) (*gen.User, error) {
	if !strings.HasPrefix(token, SessionPrefix) {
		return nil, ErrNoSession
	}
	var userID uuid.UUID
	var expires time.Time
	var revoked *time.Time
	err := s.DB.QueryRow(ctx, `SELECT user_id, expires_at, revoked_at FROM user_session WHERE token_hash = $1`, hash(token)).Scan(&userID, &expires, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	if revoked != nil || !now.Before(expires) {
		return nil, ErrNoSession
	}
	_, _ = s.DB.Exec(ctx, `UPDATE user_session SET last_seen_at = $2 WHERE token_hash = $1`, hash(token), now)
	return s.User(ctx, userID)
}

// User loads a user.
func (s *Service) User(ctx context.Context, id uuid.UUID) (*gen.User, error) {
	return LoadUser(ctx, s.DB, id)
}

func LoadUser(ctx context.Context, q db.DBTX, id uuid.UUID) (*gen.User, error) {
	var u gen.User
	var avatar *string
	err := q.QueryRow(ctx, `SELECT id, email, display_name, avatar_url, created_at FROM app_user WHERE id = $1`, id).
		Scan(&u.Id, &u.Email, &u.DisplayName, &avatar, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, err
	}
	u.AvatarUrl = tasks.NullString(avatar)
	return &u, nil
}

// Me is GET /me.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*gen.Me, error) {
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	me := &gen.Me{User: *u, PendingInvites: []gen.InvitePreview{}}
	rows, err := s.DB.Query(ctx, `
		SELECT w.id, w.name, w.slug, w.created_at, w.updated_at, m.role FROM member m JOIN workspace w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, err
	}
	me.Workspaces = make([]struct {
		CreatedAt time.Time          `json:"created_at"`
		Id        openapi_types.UUID `json:"id"`
		MyRole    gen.MemberRole     `json:"my_role"`
		Name      string             `json:"name"`
		Slug      string             `json:"slug"`
		UpdatedAt time.Time          `json:"updated_at"`
	}, 0)
	for rows.Next() {
		var w struct {
			CreatedAt time.Time          `json:"created_at"`
			Id        openapi_types.UUID `json:"id"`
			MyRole    gen.MemberRole     `json:"my_role"`
			Name      string             `json:"name"`
			Slug      string             `json:"slug"`
			UpdatedAt time.Time          `json:"updated_at"`
		}
		var role string
		if err := rows.Scan(&w.Id, &w.Name, &w.Slug, &w.CreatedAt, &w.UpdatedAt, &role); err != nil {
			rows.Close()
			return nil, err
		}
		w.MyRole = gen.MemberRole(role)
		me.Workspaces = append(me.Workspaces, w)
	}
	rows.Close()
	irows, err := s.DB.Query(ctx, `
		SELECT i.token FROM workspace_invite i WHERE lower(i.email) = lower($1) AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > $2
		  AND NOT EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = i.workspace_id AND m.user_id = $3)`, u.Email, s.Clock.Now(), userID)
	if err != nil {
		return nil, err
	}
	var tokens []string
	for irows.Next() {
		var t string
		if err := irows.Scan(&t); err == nil {
			tokens = append(tokens, t)
		}
	}
	irows.Close()
	for _, t := range tokens {
		if p, err := s.PreviewInvite(ctx, t); err == nil {
			me.PendingInvites = append(me.PendingInvites, *p)
		}
	}
	return me, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// Slugify derives a slug from a name.
func Slugify(name string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		out = "ws"
	}
	return out
}

// slugStem is the slug a new workspace starts from. Slugify folds every name
// without ASCII letters or digits to the same "ws" ("마케팅팀", "개발팀", …), so
// unrelated workspaces would all queue up behind one stem and its -2, -3 …
// suffixes. Such a name gets a short digest of itself instead: deterministic
// (the same name always proposes the same slug) but distinct per name (G3 S-6).
// The stem is kept short enough that a suffix still fits in 40 characters.
func slugStem(name string) string {
	name = strings.TrimSpace(name)
	stem := Slugify(name)
	if stem == "ws" && !hasASCIIAlnum(name) {
		sum := sha256.Sum256([]byte(name))
		stem = "ws-" + hex.EncodeToString(sum[:3])
	}
	if len(stem) > 31 {
		stem = strings.Trim(stem[:31], "-")
	}
	return stem
}

func hasASCIIAlnum(s string) bool {
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// CreateWorkspace creates workspace + settings row + owner membership.
func (s *Service) CreateWorkspace(ctx context.Context, userID uuid.UUID, name string, slug *string) (*gen.Workspace, error) {
	if strings.TrimSpace(name) == "" || len(name) > 80 {
		return nil, apperr.Validation(apperr.Field("name", "length", "name must be 1–80 characters"))
	}
	sl := slugStem(name)
	explicit := slug != nil && *slug != ""
	if explicit {
		if !slugRe.MatchString(*slug) {
			return nil, apperr.Validation(apperr.Field("slug", "pattern", "slug must match ^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$"))
		}
		sl = *slug
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var w gen.Workspace
	// The slug suffix retry runs inside a savepoint: a failed INSERT aborts the
	// enclosing transaction otherwise, so the second workspace with the same
	// name died on `25P02 current transaction is aborted` instead of becoming
	// "…-2" (G3 S-6, the same shape as runtimes.Pair / S-4). After a few
	// numbered suffixes a random tail settles a crowded stem in one round trip.
	for i := 0; ; i++ {
		candidate := sl
		switch {
		case i == 0:
		case i <= 8:
			candidate = fmt.Sprintf("%s-%d", sl, i+1)
		default:
			candidate = fmt.Sprintf("%s-%s", sl, randomHex(4))
		}
		sp, err2 := tx.Begin(ctx) // pgx nested tx = SAVEPOINT
		if err2 != nil {
			return nil, err2
		}
		err = sp.QueryRow(ctx, `INSERT INTO workspace (name, slug, created_at, updated_at) VALUES ($1, $2, $3, $3) RETURNING id, name, slug, created_at, updated_at`,
			strings.TrimSpace(name), candidate, now).Scan(&w.Id, &w.Name, &w.Slug, &w.CreatedAt, &w.UpdatedAt)
		if isUnique(err) {
			_ = sp.Rollback(ctx) // ROLLBACK TO SAVEPOINT; the outer tx stays usable
			if explicit {
				return nil, apperr.Conflict("slug_taken", "this slug is already in use")
			}
			if i < 20 {
				continue
			}
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			return nil, fmt.Errorf("auth: create workspace: %w", err)
		}
		if err := sp.Commit(ctx); err != nil {
			return nil, err
		}
		break
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workspace_settings (workspace_id) VALUES ($1)`, w.Id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`, w.Id, userID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Service) ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]gen.Workspace, error) {
	rows, err := s.DB.Query(ctx, `SELECT w.id, w.name, w.slug, w.created_at, w.updated_at FROM member m JOIN workspace w ON w.id = m.workspace_id WHERE m.user_id = $1 ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gen.Workspace{}
	for rows.Next() {
		var w gen.Workspace
		if err := rows.Scan(&w.Id, &w.Name, &w.Slug, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Service) GetWorkspace(ctx context.Context, id uuid.UUID) (*gen.Workspace, error) {
	var w gen.Workspace
	err := s.DB.QueryRow(ctx, `SELECT id, name, slug, created_at, updated_at FROM workspace WHERE id = $1`, id).Scan(&w.Id, &w.Name, &w.Slug, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("workspace")
	}
	return &w, err
}

// Membership is the caller's role in a workspace.
type Membership struct {
	MemberID uuid.UUID
	Role     string
}

// Member returns the membership or nil when the user is not a member.
func (s *Service) Member(ctx context.Context, wsID, userID uuid.UUID) (*Membership, error) {
	var m Membership
	err := s.DB.QueryRow(ctx, `SELECT id, role FROM member WHERE workspace_id = $1 AND user_id = $2`, wsID, userID).Scan(&m.MemberID, &m.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

func (s *Service) ListMembers(ctx context.Context, wsID uuid.UUID, cursor *string, limit int) ([]gen.Member, *string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{wsID, limit + 1}
	where := ""
	if cursor != nil {
		if cid, err := uuid.Parse(*cursor); err == nil {
			args = append(args, cid)
			where = " AND (m.created_at, m.id) > (SELECT created_at, id FROM member WHERE id = $3)"
		}
	}
	rows, err := s.DB.Query(ctx, `
		SELECT m.id, m.workspace_id, m.role, m.created_at, u.id, u.email, u.display_name, u.avatar_url, u.created_at
		FROM member m JOIN app_user u ON u.id = m.user_id WHERE m.workspace_id = $1`+where+` ORDER BY m.created_at, m.id LIMIT $2`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []gen.Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, *m)
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		c := out[len(out)-1].Id.String()
		next = &c
	}
	return out, next, rows.Err()
}

func scanMember(row pgx.Row) (*gen.Member, error) {
	var m gen.Member
	var role string
	var avatar *string
	if err := row.Scan(&m.Id, &m.WorkspaceId, &role, &m.CreatedAt, &m.User.Id, &m.User.Email, &m.User.DisplayName, &avatar, &m.User.CreatedAt); err != nil {
		return nil, err
	}
	m.Role = gen.MemberRole(role)
	m.User.AvatarUrl = tasks.NullString(avatar)
	return &m, nil
}

// CreateInvite issues an invite link (owner/admin).
func (s *Service) CreateInvite(ctx context.Context, wsID, userID uuid.UUID, email *string, role string, expiresInHours int) (*gen.Invite, error) {
	if role == "" {
		role = "member"
	}
	if role == "owner" {
		return nil, apperr.Validation(apperr.Field("role", "invalid", "owner cannot be granted by invite"))
	}
	if expiresInHours <= 0 {
		expiresInHours = 168
	}
	if expiresInHours > 720 {
		return nil, apperr.Validation(apperr.Field("expires_in_hours", "maximum", "at most 720 hours"))
	}
	if email != nil {
		e := strings.ToLower(strings.TrimSpace(*email))
		if e == "" {
			email = nil
		} else if !emailRe.MatchString(e) {
			return nil, apperr.Validation(apperr.Field("email", "format", "invalid email"))
		} else {
			email = &e
		}
	}
	token := random("inv_", 24)
	now := s.Clock.Now()
	var id uuid.UUID
	if err := s.DB.QueryRow(ctx, `
		INSERT INTO workspace_invite (workspace_id, email, role, token, invited_by, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, wsID, email, role, token, userID, now.Add(time.Duration(expiresInHours)*time.Hour), now).Scan(&id); err != nil {
		return nil, fmt.Errorf("auth: create invite: %w", err)
	}
	return s.getInvite(ctx, id)
}

func (s *Service) getInvite(ctx context.Context, id uuid.UUID) (*gen.Invite, error) {
	rows, err := s.DB.Query(ctx, selectInvite+` WHERE i.id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, apperr.NotFound("invite")
	}
	return s.scanInvite(rows)
}

const selectInvite = `
	SELECT i.id, i.workspace_id, i.email, i.role, i.token, i.expires_at, i.created_at, i.accepted_at, i.revoked_at,
	       u.id, u.email, u.display_name, u.avatar_url, u.created_at
	FROM workspace_invite i JOIN app_user u ON u.id = i.invited_by`

func (s *Service) scanInvite(row pgx.Row) (*gen.Invite, error) {
	var inv gen.Invite
	var email, avatar *string
	var role string
	var accepted, revoked *time.Time
	var by gen.User
	if err := row.Scan(&inv.Id, &inv.WorkspaceId, &email, &role, &inv.Token, &inv.ExpiresAt, &inv.CreatedAt, &accepted, &revoked,
		&by.Id, &by.Email, &by.DisplayName, &avatar, &by.CreatedAt); err != nil {
		return nil, err
	}
	inv.Role = gen.MemberRole(role)
	if email != nil {
		inv.Email = nullableEmail(*email)
	}
	inv.AcceptedAt = tasks.NullTime(accepted)
	by.AvatarUrl = tasks.NullString(avatar)
	inv.InvitedBy = &by
	inv.Url = strings.TrimRight(s.BaseURL, "/") + "/invite/" + inv.Token
	switch {
	case revoked != nil:
		inv.Status = gen.InviteStatusRevoked
	case accepted != nil:
		inv.Status = gen.InviteStatusAccepted
	case !s.Clock.Now().Before(inv.ExpiresAt):
		inv.Status = gen.InviteStatusExpired
	default:
		inv.Status = gen.InviteStatusPending
	}
	return &inv, nil
}

func (s *Service) ListInvites(ctx context.Context, wsID uuid.UUID) ([]gen.Invite, error) {
	rows, err := s.DB.Query(ctx, selectInvite+` WHERE i.workspace_id = $1 ORDER BY i.created_at DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gen.Invite{}
	for rows.Next() {
		inv, err := s.scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	return out, rows.Err()
}

func (s *Service) RevokeInvite(ctx context.Context, wsID, id uuid.UUID) error {
	tag, err := s.DB.Exec(ctx, `UPDATE workspace_invite SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND workspace_id = $2`, id, wsID, s.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("invite")
	}
	return nil
}

// PreviewInvite is the public S3 lookup: 404 unknown, 410 expired/revoked.
func (s *Service) PreviewInvite(ctx context.Context, token string) (*gen.InvitePreview, error) {
	rows, err := s.DB.Query(ctx, selectInvite+` WHERE i.token = $1`, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, apperr.NotFound("invite")
	}
	inv, err := s.scanInvite(rows)
	if err != nil {
		return nil, err
	}
	rows.Close()
	switch inv.Status {
	case gen.InviteStatusRevoked:
		return nil, apperr.Gone("invite_revoked", "this invite was revoked")
	case gen.InviteStatusExpired:
		return nil, apperr.Gone("invite_expired", "this invite has expired")
	case gen.InviteStatusAccepted:
		return nil, apperr.Gone("invite_used", "this invite was already accepted")
	}
	w, err := s.GetWorkspace(ctx, inv.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return &gen.InvitePreview{Token: inv.Token, Workspace: *w, Role: inv.Role, InvitedBy: *inv.InvitedBy, ExpiresAt: inv.ExpiresAt}, nil
}

// AcceptInvite joins the workspace (idempotent: existing membership returned).
func (s *Service) AcceptInvite(ctx context.Context, token string, userID uuid.UUID) (*gen.Member, error) {
	rows, err := s.DB.Query(ctx, selectInvite+` WHERE i.token = $1`, token)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		rows.Close()
		return nil, apperr.NotFound("invite")
	}
	inv, err := s.scanInvite(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if existing, err := s.member(ctx, inv.WorkspaceId, userID); err == nil && existing != nil {
		return existing, nil
	}
	switch inv.Status {
	case gen.InviteStatusRevoked:
		return nil, apperr.Gone("invite_revoked", "this invite was revoked")
	case gen.InviteStatusExpired:
		return nil, apperr.Gone("invite_expired", "this invite has expired")
	case gen.InviteStatusAccepted:
		return nil, apperr.Gone("invite_used", "this invite was already accepted")
	}
	if inv.Email.IsSpecified() && !inv.Email.IsNull() {
		u, err := s.User(ctx, userID)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(string(u.Email), string(inv.Email.MustGet())) {
			return nil, apperr.Forbidden("invite_email_mismatch", "this invite was issued to a different email")
		}
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// S-10: two clicks on the same invite race between the membership check
	// above and this insert. member has UNIQUE (workspace_id, user_id), so the
	// loser used to get a 23505 500. Accepting twice is not an error — the
	// second acceptance is a no-op and the caller still gets its membership.
	if _, err := tx.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		inv.WorkspaceId, userID, string(inv.Role), now); err != nil {
		return nil, fmt.Errorf("auth: accept invite: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workspace_invite SET accepted_at = $2, accepted_by = $3 WHERE id = $1`, inv.Id, now, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.member(ctx, inv.WorkspaceId, userID)
}

func (s *Service) member(ctx context.Context, wsID, userID uuid.UUID) (*gen.Member, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT m.id, m.workspace_id, m.role, m.created_at, u.id, u.email, u.display_name, u.avatar_url, u.created_at
		FROM member m JOIN app_user u ON u.id = m.user_id WHERE m.workspace_id = $1 AND m.user_id = $2`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	return scanMember(rows)
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
