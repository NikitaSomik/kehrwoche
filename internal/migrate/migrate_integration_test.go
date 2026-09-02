//go:build integration

package migrate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nikitasomusev/kehrwoche/internal/migrate"
	"github.com/nikitasomusev/kehrwoche/internal/pgtest"
)

func tableExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, name,
	).Scan(&n); err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	return n == 1
}

func TestApply_FreshDatabase(t *testing.T) {
	conn := pgtest.Raw(t)
	ctx := context.Background()

	applied, err := migrate.Apply(ctx, conn, pgtest.MigrationsDir(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 || applied[0] != "0001_init.sql" {
		t.Fatalf("applied = %v, want [0001_init.sql]", applied)
	}

	if !tableExists(t, conn, "schedules") {
		t.Error("schedules table was not created")
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_migrations has %d rows, want 1", n)
	}
}

func TestApply_Idempotent(t *testing.T) {
	conn := pgtest.Raw(t)
	ctx := context.Background()

	if _, err := migrate.Apply(ctx, conn, pgtest.MigrationsDir(t)); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	applied, err := migrate.Apply(ctx, conn, pgtest.MigrationsDir(t))
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second Apply reported %v, want nothing re-applied", applied)
	}
}

func TestApply_BadMigrationRollsBack(t *testing.T) {
	conn := pgtest.Raw(t)
	ctx := context.Background()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001_broken.sql"), []byte("CREATE TABLE oops ("), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := migrate.Apply(ctx, conn, dir); err == nil {
		t.Fatal("Apply returned nil on invalid SQL, want an error")
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if n != 0 {
		t.Errorf("schema_migrations recorded %d rows after a failed migration, want 0", n)
	}
	if tableExists(t, conn, "oops") {
		t.Error("partial table 'oops' survived a rolled-back migration")
	}
}
