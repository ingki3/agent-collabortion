// Package db owns the Postgres connection pool (pgx v5) and the embedded
// schema migration runner for the Colab server (PLAN.md §3 P0-a).
//
// It deliberately contains no domain queries: those arrive with P1 alongside
// the router. What lives here is only what every later stream needs on day one —
// a pool and a way to get the schema to the current version.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options is what the server exposes of the pool (S-14): a download holds a
// connection for the whole response (artifacts.Open reads the large object
// inside a transaction), so the pool's size — pgx's default is
// max(4, NumCPU) — is the number of slow downloads that can run before every
// other request waits. Zero keeps pgx's default.
type Options struct {
	MaxConns int32
	MinConns int32
}

// Open parses url, builds a pgx pool and waits until the database accepts a
// connection or ctx expires. Postgres started moments ago by `make db` may
// still be booting, so a failed Ping is retried instead of returned.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return OpenWith(ctx, url, Options{})
}

// OpenWith is Open with the pool sized by opts.
func OpenWith(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	if opts.MinConns > 0 {
		cfg.MinConns = opts.MinConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	if err := waitReady(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func waitReady(ctx context.Context, pool *pgxpool.Pool) error {
	const step = 500 * time.Millisecond
	var last error
	for {
		if last = pool.Ping(ctx); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("db: not ready: %w (last error: %v)", ctx.Err(), last)
		case <-time.After(step):
		}
	}
}

// Migrate opens a pool for url, applies every pending migration and closes the
// pool again. It is idempotent: running it against an up-to-date database is a
// no-op. The number of migrations applied in this call is returned.
func Migrate(ctx context.Context, url string) (int, error) {
	pool, err := Open(ctx, url)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	return MigratePool(ctx, pool)
}

// ErrDirtyMigration is returned when an already-applied migration file differs
// from the version recorded in schema_migrations.
var ErrDirtyMigration = errors.New("db: applied migration was modified")
