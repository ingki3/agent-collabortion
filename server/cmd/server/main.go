// Command server is the Colab API server: router, session/lane/task state
// machines, queue, realtime, inbox, GC scheduler (PLAN.md §2 stream S).
//
// P0-a skeleton: boots, logs, serves /healthz. Everything else lands after G2.
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
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/obs"
)

func main() {
	log := obs.NewLogger("server")
	addr := envOr("COLAB_SERVER_ADDR", ":8080")

	// Schema first (PLAN.md §3 P0-a). Without COLAB_DB_URL the server still
	// serves /healthz so the skeleton keeps working; with it, a failed
	// migration is fatal — running against an unknown schema is worse than
	// not running.
	if dbURL := os.Getenv("COLAB_DB_URL"); dbURL == "" {
		log.Warn("COLAB_DB_URL not set; skipping migrations, running without a database")
	} else {
		mctx, mcancel := context.WithTimeout(context.Background(), 60*time.Second)
		n, err := db.Migrate(mctx, dbURL)
		mcancel()
		if err != nil {
			log.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		log.Info("schema up to date", "migrations_applied", n)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"contracts":"` + contracts.Version + `"}`))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", addr, "contracts", contracts.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
