// Command migrate applies the embedded schema migrations to the database at
// COLAB_DB_URL (or -url) and exits. `make migrate` uses it; the server binary
// also runs the same code at startup when COLAB_DB_URL is set.
package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/obs"
)

func main() {
	log := obs.NewLogger("migrate")
	url := flag.String("url", os.Getenv("COLAB_DB_URL"), "Postgres URL (default $COLAB_DB_URL)")
	timeout := flag.Duration("timeout", 60*time.Second, "give up waiting for the database after this long")
	flag.Parse()

	if *url == "" {
		log.Error("no database url: set COLAB_DB_URL or pass -url")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	n, err := db.Migrate(ctx, *url)
	if err != nil {
		log.Error("migrate failed", "applied", n, "err", err)
		os.Exit(1)
	}
	log.Info("migrations up to date", "applied", n)
}
