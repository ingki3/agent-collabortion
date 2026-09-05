// Package httpapi serves contracts/openapi.yaml (generated router in gen/)
// for the P1 operation set, the /v1/daemon/* protocol from
// contracts/daemon-protocol.md, and the SSE stream. Everything outside P1
// answers 501 not_implemented (unimplemented.go).
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/events"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// BasePath is the OpenAPI servers[0].url.
const BasePath = "/api/v1"

// Server implements gen.ServerInterface for P1 and the daemon endpoints.
type Server struct {
	unimplemented

	DB       *pgxpool.Pool
	Clock    clock.Clock
	Log      *slog.Logger
	Auth     *auth.Service
	Agents   *agents.Service
	Runtimes *runtimes.Service
	Sessions *sessions.Service
	Router   *router.Service
	Tasks    *tasks.Service
	Events   *events.Service
	Queue    *queue.Postgres
	Tokens   *tokens.Service
	Hub      *realtime.Hub

	// SecureCookies sets the Secure flag on the session cookie (HTTPS).
	SecureCookies bool
}

// Deps builds every service on one pool and clock (used by main and tests).
type Deps struct {
	DB        *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	ServerURL string
}

// NewServer wires the services.
func NewServer(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	hub := realtime.New(d.DB, d.Clock)
	tok := tokens.New(d.Clock)
	tsk := tasks.New(d.DB, d.Clock, tok, hub)
	notifier := queue.NewNotifier()
	q := queue.NewPostgres(d.DB, d.Clock, tsk, notifier)
	rt := router.New(d.DB, d.Clock, hub, notifier)
	return &Server{
		DB: d.DB, Clock: d.Clock, Log: d.Log,
		Auth:     auth.New(d.DB, d.Clock, d.ServerURL),
		Agents:   agents.New(d.DB, d.Clock),
		Runtimes: runtimes.New(d.DB, d.Clock, hub, d.ServerURL),
		Sessions: sessions.New(d.DB, d.Clock, hub, rt),
		Router:   rt,
		Tasks:    tsk,
		Events:   events.New(d.DB, d.Clock, hub),
		Queue:    q,
		Tokens:   tok,
		Hub:      hub,
	}
}

// Handler returns the full HTTP handler: generated OpenAPI router under
// /api/v1, the daemon protocol under /v1/daemon, /healthz.
func (s *Server) Handler() http.Handler {
	api := gen.HandlerWithOptions(s, gen.StdHTTPServerOptions{
		BaseURL: BasePath,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, validationFromBind(err))
		},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "contracts": contracts.Version})
	})
	// postMessage is registered by hand: colab-cli.md §2.2 uses
	// `<task_id>:<attempt>:<seq>` as Idempotency-Key while openapi.yaml types
	// the header as uuid; the generated binder would reject the CLI key.
	mux.HandleFunc("POST "+BasePath+"/sessions/{sessionId}/messages", s.postMessage)
	s.daemonRoutes(mux)
	mux.Handle("/", api)
	return s.authenticate(mux)
}

func validationFromBind(err error) *Problem {
	msg := err.Error()
	code := "invalid_parameter"
	if strings.Contains(msg, "Idempotency-Key") {
		code = "idempotency_key_required"
	}
	return &Problem{Status: http.StatusUnprocessableEntity, Code: code, Title: "Validation failed", Detail: msg,
		Errors: []apperr.FieldError{{Field: "params", Code: code, Message: msg}}}
}
