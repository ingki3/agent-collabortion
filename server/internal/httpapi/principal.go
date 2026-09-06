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
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Principal is who is calling: a person (UserSession) or an agent attempt
// (TaskToken). Daemon tokens are resolved separately in daemon.go.
type Principal struct {
	User         *gen.User
	SessionToken string
	Task         *tokens.Scope
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
		p := &Principal{}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			switch {
			case strings.HasPrefix(tok, tokens.Prefix):
				sc, err := s.Tokens.Verify(r.Context(), s.DB, tok)
				switch {
				case errors.Is(err, tokens.ErrRevoked):
					writeProblem(w, apperr.Unauthorized("token_revoked", "이 task는 재큐잉되어 토큰이 폐기되었다. 즉시 종료하라."))
					return
				case errors.Is(err, tokens.ErrExpired):
					writeProblem(w, apperr.Unauthorized("token_expired", "task token expired"))
					return
				case err != nil:
					writeProblem(w, apperr.Unauthorized("invalid_token", "unknown task token"))
					return
				}
				p.Task = sc
			default:
				u, err := s.Auth.Resolve(r.Context(), tok)
				if err != nil {
					writeProblem(w, apperr.Unauthorized("unauthorized", "invalid session token"))
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
			return nil, apperr.Forbidden("task_token_scope", "this operation needs a user session (colab-cli.md §1 token scope)")
		}
		return nil, apperr.Unauthorized("unauthorized", "login required")
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
		return nil, nil, apperr.Forbidden("not_member", "not a member of this workspace")
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
		return nil, apperr.Forbidden("admin_required", "owner or admin role required")
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
			return nil, apperr.Forbidden("outside_task_scope", "task token cannot access another session")
		}
		return nil, nil
	}
	if p.User == nil {
		return nil, apperr.Unauthorized("unauthorized", "login required")
	}
	m, err := s.Auth.Member(r.Context(), wsID, p.User.Id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if m == nil {
		return nil, apperr.NotFound("session") // do not reveal other workspaces' sessions
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
	err := s.DB.QueryRow(r.Context(), `SELECT workspace_id, director_user_id FROM session WHERE id = $1`, sessionID).
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
		return nil, uuid.Nil, apperr.Forbidden("director_required", "only the session's Director can end the session")
	}
	return u, wsID, nil
}
