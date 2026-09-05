package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/migrations"
)

// migrationLockKey is the pg_advisory_lock key that serialises concurrent
// runners (two server replicas starting at once must not both apply 0001).
const migrationLockKey int64 = 0x0c01ab_5c4e_4a01

var fileNameRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one embedded SQL file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string // hex sha256 of SQL
}

// Load reads and orders the embedded migrations. Exported so tests and tooling
// can inspect the set without a database.
func Load() ([]Migration, error) {
	return load(migrations.FS)
}

func load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("db: migration %q does not match NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("db: duplicate migration version %04d: %s and %s", v, prev, e.Name())
		}
		seen[v] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("db: read %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// MigratePool applies pending migrations using an existing pool.
//
// Each migration runs in its own transaction together with its
// schema_migrations row, so a failure leaves the database at the previous
// version. Already-applied versions are compared by checksum and a mismatch
// aborts with ErrDirtyMigration.
func MigratePool(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	all, err := Load()
	if err != nil {
		return 0, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return 0, fmt.Errorf("db: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    integer     PRIMARY KEY,
			name       text        NOT NULL,
			checksum   text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return 0, fmt.Errorf("db: ensure schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return 0, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return 0, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("db: read schema_migrations: %w", err)
	}

	n := 0
	for _, m := range all {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return n, fmt.Errorf("%w: %04d_%s (recorded %s, embedded %s)", ErrDirtyMigration, m.Version, m.Name, sum[:12], m.Checksum[:12])
			}
			continue
		}
		if err := applyOne(ctx, conn.Conn(), m); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin %04d: %w", m.Version, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// No arguments → simple protocol → the whole multi-statement file runs as one batch.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("db: apply %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("db: record %04d: %w", m.Version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit %04d: %w", m.Version, err)
	}
	return nil
}

// Version returns the highest applied migration version, or 0 when the
// schema_migrations table is missing or empty.
func Version(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var v *int
	err := pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		if isUndefinedTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("db: version: %w", err)
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

func isUndefinedTable(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "42P01"
}
