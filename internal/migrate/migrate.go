// Package migrate applies the SQL files in a migrations directory to a Postgres
// database, in filename order, each in its own transaction, recording every
// applied file in a schema_migrations table. cmd/migrate is a thin CLI wrapper;
// integration tests call Apply directly.
package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Apply runs every *.sql file in dir that is not yet recorded in
// schema_migrations, in filename order. It returns the versions applied by this
// call — empty when the database was already up to date. A failing file leaves
// the database untouched from that file onward (each file runs in its own
// transaction).
func Apply(ctx context.Context, conn *pgx.Conn, dir string) ([]string, error) {
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("load applied versions: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)

	var newly []string
	for _, f := range files {
		version := filepath.Base(f)
		if applied[version] {
			continue
		}
		body, err := os.ReadFile(f) //nolint:gosec // f comes from a caller-controlled migrations dir glob, not user input
		if err != nil {
			return newly, err
		}
		if err := applyOne(ctx, conn, version, string(body)); err != nil {
			return newly, err
		}
		newly = append(newly, version)
	}
	return newly, nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, version, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("apply %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", version, err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
