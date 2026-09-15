// Package testdb gives integration tests a private, freshly migrated Postgres
// database so packages can run in parallel without stepping on each other
// (server/internal/db's own test drops schema public on the shared database).
//
// Tests skip when COLAB_TEST_DB_URL is unset.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// New creates database colab_t_<random> on the server COLAB_TEST_DB_URL points
// at, migrates it and returns a pool. The database is dropped on cleanup.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("COLAB_TEST_DB_URL") == "" {
		t.Skip("COLAB_TEST_DB_URL not set; skipping integration test")
	}
	pool, drop, err := Provision(context.Background())
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	t.Cleanup(drop)
	return pool
}

// ErrNoTestDB is Provision's answer when COLAB_TEST_DB_URL is unset. It is a
// value rather than a skip because Provision has no *testing.T — a TestMain
// (server/test/sim) needs to decide for the whole package.
var ErrNoTestDB = errors.New("testdb: COLAB_TEST_DB_URL not set")

// Provision is New without the testing hooks: it creates and migrates a
// private database and returns the pool plus the function that drops it.
func Provision(ctx context.Context) (*pgxpool.Pool, func(), error) {
	base := os.Getenv("COLAB_TEST_DB_URL")
	if base == "" {
		return nil, nil, ErrNoTestDB
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	u, err := url.Parse(base)
	if err != nil {
		return nil, nil, fmt.Errorf("testdb: parse url: %w", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "colab_t_" + hex.EncodeToString(b[:])

	admin, err := db.Open(ctx, base)
	if err != nil {
		return nil, nil, fmt.Errorf("testdb: open admin: %w", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("testdb: create database: %w", err)
	}
	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("testdb: open %s: %w", name, err)
	}
	if _, err := db.MigratePool(ctx, pool); err != nil {
		pool.Close()
		admin.Close()
		return nil, nil, fmt.Errorf("testdb: migrate: %w", err)
	}
	return pool, func() {
		pool.Close()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, _ = admin.Exec(cctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		admin.Close()
	}, nil
}

// URL returns the connection string of pool's database (for code that opens
// its own pool).
func URL(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	cfg := pool.Config().ConnConfig
	base := os.Getenv("COLAB_TEST_DB_URL")
	u, _ := url.Parse(base)
	u.Path = "/" + strings.TrimPrefix(cfg.Database, "/")
	return u.String()
}
