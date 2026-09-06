//go:build integration

package migrate_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NikitaSomik/kehrwoche/internal/migrate"
	"github.com/NikitaSomik/kehrwoche/internal/pgtest"
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

// migrationNames is whatever migrations/ actually holds, so adding one doesn't
// break this test.
func migrationNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(pgtest.MigrationsDir(t), "*.sql"))
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no migrations found")
	}
	slices.Sort(paths)
	names := make([]string, len(paths))
	for i, path := range paths {
		names[i] = filepath.Base(path)
	}
	return names
}

func TestApply_FreshDatabase(t *testing.T) {
	conn := pgtest.Raw(t)
	ctx := context.Background()

	want := migrationNames(t)

	applied, err := migrate.Apply(ctx, conn, pgtest.MigrationsDir(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !slices.Equal(applied, want) {
		t.Fatalf("applied = %v, want %v", applied, want)
	}

	for _, table := range []string{"schedules", "users"} {
		if !tableExists(t, conn, table) {
			t.Errorf("%s table was not created", table)
		}
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if n != len(want) {
		t.Errorf("schema_migrations has %d rows, want %d", n, len(want))
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
