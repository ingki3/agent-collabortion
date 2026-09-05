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
		pool, err := db.Open(ctx, dbURL)
		if err != nil {
			log.Error("db open failed", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
		srv := httpapi.NewServer(httpapi.Deps{DB: pool, Clock: clock.Real{}, Log: log, ServerURL: serverURL})
		srv.SecureCookies = os.Getenv("COLAB_SECURE_COOKIES") == "1"
		handler = srv.Handler()
		go scheduler(ctx, srv, log)
	}

	httpSrv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
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
// stream_event retention, idempotency retention).
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
		case <-purge.C:
			if err := srv.Hub.Purge(ctx); err != nil {
				log.Warn("stream purge", "err", err)
			}
			if err := srv.PurgeIdempotency(ctx); err != nil {
				log.Warn("idempotency purge", "err", err)
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
