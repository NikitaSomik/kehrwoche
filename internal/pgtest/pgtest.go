// Package pgtest gives integration tests a throwaway Postgres connection.
//
// It reads TEST_DATABASE_URL (set by `task test:integration` locally, or by the
// CI service container). Tests that call it are skipped when the variable is
// unset, so `go test ./...` without the `integration` build tag stays green
// with no database.
//
// The pointed-at database is treated as disposable: helpers drop and recreate
// the application schema. Never point TEST_DATABASE_URL at a real database.
package pgtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nikitasomusev/kehrwoche/internal/migrate"
)

// Raw returns a connection to a pristine database: the schedules and
// schema_migrations tables are dropped, nothing is migrated. Use it to test the
// migrator itself. Skips the test when TEST_DATABASE_URL is unset; closes the
// connection on cleanup.
func Raw(t *testing.T) *pgx.Conn {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS schedules, schema_migrations`); err != nil {
		t.Fatalf("pgtest: reset schema: %v", err)
	}
	return conn
}

// Connect returns a connection to a freshly migrated, empty database. Use it
// for repo and seed tests.
func Connect(t *testing.T) *pgx.Conn {
	t.Helper()

	conn := Raw(t)
	if _, err := migrate.Apply(context.Background(), conn, MigrationsDir(t)); err != nil {
		t.Fatalf("pgtest: apply migrations: %v", err)
	}
	return conn
}

// MigrationsDir walks up from the test's working directory to the module root
// (the directory holding go.mod) and returns its migrations/ path, so callers
// don't hard-code a relative path that breaks when tests move.
func MigrationsDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("pgtest: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("pgtest: could not locate module root (no go.mod above the test directory)")
		}
		dir = parent
	}
}
