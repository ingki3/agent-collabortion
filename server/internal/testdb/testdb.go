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
	base := os.Getenv("COLAB_TEST_DB_URL")
	if base == "" {
		t.Skip("COLAB_TEST_DB_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testdb: parse url: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "colab_t_" + hex.EncodeToString(b[:])

	admin, err := db.Open(ctx, base)
	if err != nil {
		t.Fatalf("testdb: open admin: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("testdb: create database: %v", err)
	}

	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		admin.Close()
		t.Fatalf("testdb: open %s: %v", name, err)
	}
	if _, err := db.MigratePool(ctx, pool); err != nil {
		pool.Close()
		admin.Close()
		t.Fatalf("testdb: migrate: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, _ = admin.Exec(cctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		admin.Close()
	})
	return pool
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
