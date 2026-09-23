package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/install"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Principal is who is calling: a person (UserSession), an agent attempt
// (TaskToken) or — for exactly one operation — a paired machine (DaemonToken).
// The `/v1/daemon/*` surface still resolves its own token in daemon.go; this
// carries the openapi half.
type Principal struct {
	User         *gen.User
	SessionToken string
	Task         *tokens.Scope
	Daemon       *DaemonScope
	// agentRole caches the task principal's agent role for this request
	// (Server.agentRole). Never trusted from the token — see agentRole.
	agentRole *string
}

// DaemonScope is a verified `cdt_` bearer on the openapi surface.
//
// S-57: openapi v0.7.3 adds `DaemonToken` to `downloadArtifact` and to NOTHING
// else — §4.3's `rebind_prepare` orders the daemon to download the session's
// diff artifacts, and with no scheme for it every one of those GETs came back
// 401, so the rebind prompt pointed at a directory holding a manifest and no
// diffs (T-I4 차단 ③). The scope is checked at the operation, not here: a
// daemon principal that reaches any other handler has no user and no task, so
// the ordinary gates answer 401 exactly as before.
type DaemonScope struct {
	RuntimeID   uuid.UUID
	WorkspaceID uuid.UUID
}

type ctxKey struct{}

func principalOf(r *http.Request) *Principal {
	p, _ := r.Context().Value(ctxKey{}).(*Principal)
	if p == nil {
		return &Principal{}
	}
	return p
}

// authenticate resolves the cookie / bearer once per request. A present but
// invalid credential is rejected here (401), so a revoked task token never
// reaches a handler (FR-9.1, E11-04).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/daemon/") {
			next.ServeHTTP(w, r)
			return
		}
		// S-63: the installer is fetched by a machine that has no account and
		// no daemon token yet — it is what CREATES the daemon. It must never be
		// able to answer 401, including for a caller carrying some other,
		// stale credential (P4 리뷰: 인증 오류 하나가 다음 오류를 가린다).
		if r.URL.Path == install.Path {
			next.ServeHTTP(w, r)
			return
		}
		p := &Principal{}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			switch {
			case strings.HasPrefix(tok, runtimes.DaemonTokenPrefix):
				rt, ws, err := s.Runtimes.VerifyDaemonToken(r.Context(), tok)
				if err != nil {
					writeProblem(w, apperr.Unauthorized("invalid_daemon_token", "이 컴퓨터의 연결 토큰이 올바르지 않습니다 — 컴퓨터를 다시 연결해 주세요"))
					return
				}
				p.Daemon = &DaemonScope{RuntimeID: rt, WorkspaceID: ws}
			case strings.HasPrefix(tok, tokens.Prefix):
				sc, err := s.Tokens.Verify(r.Context(), s.DB, tok)
				switch {
				case errors.Is(err, tokens.ErrRevoked):
					writeProblem(w, apperr.Unauthorized("token_revoked", "이 할 일은 다시 배정되어 토큰이 폐기되었습니다. 지금 바로 멈추세요."))
					return
				case errors.Is(err, tokens.ErrExpired):
					writeProblem(w, apperr.Unauthorized("token_expired", "토큰이 만료되었습니다"))
					return
				case err != nil:
					writeProblem(w, apperr.Unauthorized("invalid_token", "올바르지 않은 토큰입니다"))
					return
				}
				p.Task = sc
			default:
				u, err := s.Auth.Resolve(r.Context(), tok)
				if err != nil {
					writeProblem(w, apperr.Unauthorized("unauthorized", "로그인이 만료되었습니다 — 다시 로그인해 주세요"))
					return
				}
				p.User, p.SessionToken = u, tok
			}
		} else if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
			u, err := s.Auth.Resolve(r.Context(), c.Value)
			if err == nil {
				p.User, p.SessionToken = u, c.Value
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.SecureCookies, MaxAge: int(auth.SessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.SecureCookies, MaxAge: -1})
}

// user requires a logged-in person.
func (s *Server) user(r *http.Request) (*gen.User, *Problem) {
	p := principalOf(r)
	if p.User == nil {
		if p.Task != nil {
			return nil, apperr.Forbidden("task_token_scope", "사람만 쓸 수 있는 기능입니다 — 에이전트 토큰으로는 할 수 없습니다")
		}
		return nil, apperr.Unauthorized("unauthorized", "로그인이 필요합니다")
	}
	return p.User, nil
}

// member requires workspace membership; non-members get 403.
func (s *Server) member(r *http.Request, wsID uuid.UUID) (*gen.User, *auth.Membership, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, nil, p
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, nil, apperr.Forbidden("not_member", "이 워크스페이스의 멤버가 아닙니다")
	}
	return u, m, nil
}

// admin requires owner or admin.
func (s *Server) admin(r *http.Request, wsID uuid.UUID) (*gen.User, *Problem) {
	u, m, p := s.member(r, wsID)
	if p != nil {
		return nil, p
	}
	if m.Role != "owner" && m.Role != "admin" {
		return nil, apperr.Forbidden("admin_required", "소유자·관리자만 할 수 있습니다")
	}
	return u, nil
}

// sessionAccess is Q8: a member of the session's workspace, or a task token
// scoped to exactly this session. Returns the viewer user (nil for tokens).
func (s *Server) sessionAccess(r *http.Request, sessionID uuid.UUID) (*gen.User, *Problem) {
	p := principalOf(r)
	wsID, err := s.Sessions.WorkspaceOf(r.Context(), sessionID)
	if err != nil {
		return nil, apperr.As(err)
	}
	if p.Task != nil {
		if p.Task.SessionID != sessionID {
			return nil, apperr.Forbidden("outside_task_scope", "다른 세션에는 접근할 수 없습니다")
		}
		return nil, nil
	}
	if p.User == nil {
		return nil, apperr.Unauthorized("unauthorized", "로그인이 필요합니다")
	}
	m, err := s.Auth.Member(r.Context(), wsID, p.User.Id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, apperr.NotFound("session") // do not reveal other workspaces' sessions
	}
	// The session is a room (v0.19): an invited room does not exist for a
	// member who was not invited (FR-5.3), on the old /sessions/* aliases as
	// on /rooms/* — rooms.Decide is the one table (review #291 R1-1). A task
	// token never reaches here: it is scoped to its own session above.
	a, err := rooms.LoadAccess(r.Context(), s.DB, sessionID, p.User.Id)
	if err != nil {
		pr := apperr.As(err)
		if pr.Status == http.StatusNotFound {
			return nil, apperr.NotFound("session")
		}
		return nil, pr
	}
	if !rooms.Decide(rooms.ActView, a.Standing) {
		return nil, apperr.NotFound("session")
	}
	if r.Method == http.MethodGet {
		// Reading an invited room through the old aliases is the same audit
		// look as getRoom (FR-5.3 P-Q).
		if err := s.auditView(r.Context(), a, p.User.Id); err != nil {
			return nil, apperr.Internal(err)
		}
	}
	return p.User, nil
}

// sessionDirector is the gate for session-level control (completeSession).
// Unlike lane cancellation the deputy is NOT included: ending the session is
// not the urgent stop-the-runaway action the deputy exists for (t-3), and it
// is not undoable.
func (s *Server) sessionDirector(r *http.Request, sessionID uuid.UUID) (*gen.User, uuid.UUID, *Problem) {
	u, p := s.user(r)
	if p != nil {
		return nil, uuid.Nil, p
	}
	var wsID, director uuid.UUID
	err := s.DB.QueryRow(r.Context(), `SELECT s.workspace_id, wk.director_user_id FROM room s JOIN work wk ON wk.room_id = s.id WHERE s.id = $1`, sessionID).
		Scan(&wsID, &director)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	m, err := s.Auth.Member(r.Context(), wsID, u.Id)
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, uuid.Nil, apperr.NotFound("session")
	}
	if u.Id != director {
		return nil, uuid.Nil, apperr.Forbidden("director_required", "세션은 그 세션의 Director 만 끝낼 수 있습니다")
	}
	return u, wsID, nil
}
