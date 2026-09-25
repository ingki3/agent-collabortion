// Command server is the Colab API server: OpenAPI router (P1 operations),
// daemon protocol, queue, task state machine, SSE, stale sweep
// (PLAN.md §2 stream S, plan/P1_TASKS.md T-S1).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi"
	"github.com/ingki3/agent-collabortion/server/internal/obs"
)

func main() {
	log := obs.NewLogger("server")
	addr := envOr("COLAB_SERVER_ADDR", ":8080")
	serverURL := envOr("COLAB_SERVER_URL", "http://localhost:8080")
	// Invite links open in the web UI, which may live on another origin than
	// the API (`make dev`: web :3000, server :8080). Defaults to the server.
	webURL := envOr("COLAB_WEB_URL", serverURL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	var handler http.Handler = mux
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"contracts":"` + contracts.Version + `","database":false}`))
	})

	// Schema first (PLAN.md §3 P0-a). Without COLAB_DB_URL only /healthz is
	// served; with it, a failed migration is fatal.
	if dbURL := os.Getenv("COLAB_DB_URL"); dbURL == "" {
		log.Warn("COLAB_DB_URL not set; serving /healthz only")
	} else {
		mctx, mcancel := context.WithTimeout(ctx, 60*time.Second)
		n, err := db.Migrate(mctx, dbURL)
		mcancel()
		if err != nil {
			log.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		log.Info("schema up to date", "migrations_applied", n)
		pool, err := db.OpenWith(ctx, dbURL, poolOptions(log))
		if err != nil {
			log.Error("db open failed", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
		srv := httpapi.NewServer(httpapi.Deps{DB: pool, Clock: clock.Real{}, Log: log, ServerURL: serverURL, WebURL: webURL})
		srv.SecureCookies = os.Getenv("COLAB_SECURE_COOKIES") == "1"
		handler = srv.Handler()
		go scheduler(ctx, srv, log)
	}

	httpSrv := httpServer(addr, handler, log)
	go func() {
		log.Info("listening", "addr", addr, "contracts", contracts.Version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Info("stopped")
}

// scheduler runs the time-driven sweeps (daemon-protocol §7 ExpireStale,
// §4.3 command expiry, stream_event retention, idempotency retention).
func scheduler(ctx context.Context, srv *httpapi.Server, log interface {
	Warn(string, ...any)
	Info(string, ...any)
}) {
	sweep := time.NewTicker(10 * time.Second)
	purge := time.NewTicker(time.Minute)
	defer sweep.Stop()
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			n, err := srv.Queue.ExpireStale(ctx, srv.Clock.Now())
			if err != nil {
				log.Warn("expire stale", "err", err)
			} else if n > 0 {
				log.Info("expired stale attempts", "requeued", n)
			}
			// daemon-protocol v0.8 §4.5: a test chat turn has the same 5-minute
			// and 3-minute bounds but is never requeued — it is closed with an
			// error the person sees.
			if n, err := srv.ExpireTestChatTurns(ctx); err != nil {
				log.Warn("expire test chat turns", "err", err)
			} else if n > 0 {
				log.Info("expired test chat turns", "n", n)
			}
		case <-purge.C:
			if err := srv.Hub.Purge(ctx); err != nil {
				log.Warn("stream purge", "err", err)
			}
			if err := srv.PurgeIdempotency(ctx); err != nil {
				log.Warn("idempotency purge", "err", err)
			}
			if n, err := srv.ExpireCommands(ctx); err != nil {
				log.Warn("command expiry", "err", err)
			} else if n > 0 {
				log.Info("expired unconsumed daemon commands", "n", n)
			}
			// FR-5.4: a HITL request past its deadline either proceeds with the
			// agent's proposal (question/choice under `autonomous`) or is
			// flagged overdue and keeps waiting. Nothing else moves it.
			if n, err := srv.SweepHitlDeadlines(ctx); err != nil {
				log.Warn("hitl deadline sweep", "err", err)
			} else if n > 0 {
				log.Info("hitl deadlines handled", "n", n)
			}
			// T-APPROVAL: a held completion approval whose mission's work
			// ended inside another transaction (room block, kill switch).
			if n, err := srv.Sessions.ReleaseHeldApprovals(ctx); err != nil {
				log.Warn("held approval sweep", "err", err)
			} else if n > 0 {
				log.Info("held completion approvals opened", "n", n)
			}
			// FR-2A.3 (T-R1b2): a mission past its `limits.time_limit` is
			// paused `time` and its Director asked whether to go on.
			if n, err := srv.SweepWorkTimeLimits(ctx); err != nil {
				log.Warn("mission time limit sweep", "err", err)
			} else if n > 0 {
				log.Info("missions paused for their time limit", "n", n)
			}
			// FR-9.2: a machine that has been gone longer than
			// `runtime_offline_grace` parks its sessions in
			// `paused(runtime_offline)` and asks the Director to rebind or end.
			// Without it the session queues forever and nobody is told.
			if n, err := srv.Runtimes.SweepOffline(ctx); err != nil {
				log.Warn("runtime offline sweep", "err", err)
			} else if n > 0 {
				log.Info("sessions paused for offline runtimes", "n", n)
			}
			// FR-6.4: the retention pass. It runs on the minute tick rather
			// than the 10s one because retention is measured in days — a
			// per-second pass would only add load to the same answer.
			if res, err := srv.Workdirs.SweepGC(ctx); err != nil {
				log.Warn("workdir gc sweep", "err", err)
			} else if res.Deleted > 0 || res.Blocked > 0 {
				log.Info("workdir gc", "requested", res.Deleted, "blocked", res.Blocked)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// DefaultWriteTimeout bounds how long ONE response may take to be written
// (S-14). Without it a client that stops reading holds its handler — and for
// a download, the database connection the handler's transaction owns — for
// good. The two responses that legitimately outlive it extend their own
// deadline per connection: an artifact download (httpapi.DownloadArtifact,
// sized by the body) and the SSE stream (httpapi.StreamEvents, no deadline).
const DefaultWriteTimeout = 60 * time.Second

// httpServer is the listener's configuration. COLAB_HTTP_WRITE_TIMEOUT (a Go
// duration, e.g. 90s) overrides the write bound; "0" disables it, which is
// the pre-S-14 behaviour and is logged as such. "0" is for experiments only
// (PR #260 리뷰 NN4) — a deployment never wants an unbounded write, and the
// two responses that legitimately run long already extend their own deadline.
func httpServer(addr string, handler http.Handler, log warnLogger) *http.Server {
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: DefaultWriteTimeout}
	if v := os.Getenv("COLAB_HTTP_WRITE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil || d < 0:
			log.Warn("COLAB_HTTP_WRITE_TIMEOUT ignored", "value", v, "err", err, "using", DefaultWriteTimeout)
		case d == 0:
			log.Warn("COLAB_HTTP_WRITE_TIMEOUT=0: responses have no write bound — a stalled client holds its handler and its database connection")
			srv.WriteTimeout = 0
		default:
			srv.WriteTimeout = d
		}
	}
	return srv
}

// poolOptions reads the pool size (S-14): COLAB_DB_MAX_CONNS · COLAB_DB_MIN_CONNS,
// whole numbers; unset or unparsable keeps pgx's default (max(4, NumCPU)).
func poolOptions(log warnLogger) db.Options {
	var o db.Options
	for _, e := range []struct {
		key string
		dst *int32
	}{{"COLAB_DB_MAX_CONNS", &o.MaxConns}, {"COLAB_DB_MIN_CONNS", &o.MinConns}} {
		v := os.Getenv(e.key)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			log.Warn(e.key+" ignored", "value", v, "err", err)
			continue
		}
		*e.dst = int32(n)
	}
	if o.MinConns > o.MaxConns && o.MaxConns > 0 {
		log.Warn("COLAB_DB_MIN_CONNS exceeds COLAB_DB_MAX_CONNS; min lowered", "min", o.MinConns, "max", o.MaxConns)
		o.MinConns = o.MaxConns
	}
	return o
}

type warnLogger interface{ Warn(string, ...any) }
