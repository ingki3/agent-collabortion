// Package httpapi serves contracts/openapi.yaml (generated router in gen/)
// for the P1 operation set, the /v1/daemon/* protocol from
// contracts/daemon-protocol.md, and the SSE stream. Everything outside P1
// answers 501 not_implemented (unimplemented.go).
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/agents"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/artifacts"
	"github.com/ingki3/agent-collabortion/server/internal/auth"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/events"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// BasePath is the OpenAPI servers[0].url.
const BasePath = "/api/v1"

// Server implements gen.ServerInterface for P1 and the daemon endpoints.
type Server struct {
	unimplemented

	DB        *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Auth      *auth.Service
	Agents    *agents.Service
	Artifacts *artifacts.Service
	Runtimes  *runtimes.Service
	Sessions  *sessions.Service
	// Workdirs is FR-6.4's GC scheduler (P4). It reads rows and issues the
	// `gc` commands JudgeGC asked for; the judgement itself is a pure function.
	Workdirs *workdirs.Service
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
	DB    *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	// ServerURL is the API origin (COLAB_SERVER_URL): daemon install
	// commands and the CLI point here.
	ServerURL string
	// WebURL is the origin people open in a browser (COLAB_WEB_URL): invite
	// links. Falls back to ServerURL — in `make dev` the web is :3000 and the
	// server :8080 (G3 S-5).
	WebURL string
}

// NewServer wires the services.
func NewServer(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.WebURL == "" {
		d.WebURL = d.ServerURL
	}
	hub := realtime.New(d.DB, d.Clock)
	tok := tokens.New(d.Clock)
	tsk := tasks.New(d.DB, d.Clock, tok, hub)
	// The task layer moves lane.status (claim → running, finish → done, …) but
	// cannot import internal/lanes, which imports it. The hook closes that loop
	// so S7 gets a frame for every transition (G4 2판 W5).
	tsk.LanePublish = func(ctx context.Context, q db.DBTX, laneID uuid.UUID) {
		if err := lanes.Publish(ctx, hub, q, laneID); err != nil {
			d.Log.Warn("publish lane.updated", "err", err, "lane", laneID)
		}
	}
	// Same closure trick for the agent chip: FR-1.3's derivation lives in
	// internal/sessions, which imports tasks (G4 2판 W7).
	tsk.ParticipantPublish = func(ctx context.Context, q db.DBTX, sessionID, agentID uuid.UUID) {
		if err := sessions.PublishParticipant(ctx, hub, q, sessionID, agentID); err != nil {
			d.Log.Warn("publish participant.updated", "err", err, "session", sessionID, "agent", agentID)
		}
	}
	notifier := queue.NewNotifier()
	q := queue.NewPostgres(d.DB, d.Clock, tsk, notifier)
	rt := router.New(d.DB, d.Clock, hub, notifier).WithTasks(tsk)
	return &Server{
		DB: d.DB, Clock: d.Clock, Log: d.Log,
		Auth:      auth.New(d.DB, d.Clock, d.WebURL),
		Agents:    agents.New(d.DB, d.Clock),
		Artifacts: artifacts.New(d.DB, d.Clock),
		Runtimes:  runtimes.New(d.DB, d.Clock, hub, d.ServerURL).WithLog(d.Log).WithTasks(tsk),
		// §8.5's platform client is optional on purpose: with no
		// ANTHROPIC_API_KEY the summary is composed from rows, as it was in P2,
		// and every other part of the server runs unchanged (llm.FromEnv).
		Sessions: sessions.New(d.DB, d.Clock, hub, rt).WithTasks(tsk).WithLLM(platformLLM(d.Log), d.Log),
		Workdirs: workdirs.NewService(d.DB, d.Clock, hub, d.Log),
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

// platformLLM builds the §8.5 client from the environment.
//
// Returning nil is a supported outcome, not a failure. A workspace with no
// Anthropic account must still be able to finish a session, and every isolated
// test stack would otherwise need a live key; `sessions.summarise` composes the
// summary from rows in that case, exactly as P2 did.
func platformLLM(log *slog.Logger) llm.Client {
	c := llm.FromEnv(log)
	if c == nil {
		if log != nil {
			log.Info("platform LLM not configured (ANTHROPIC_API_KEY unset) — session summaries are composed from rows")
		}
		return nil
	}
	return c
}
