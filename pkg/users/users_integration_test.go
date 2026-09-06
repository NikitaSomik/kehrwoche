//go:build integration

package users_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikitasomusev/kehrwoche/internal/pgtest"
	"github.com/nikitasomusev/kehrwoche/pkg/users"
)

// The point of the table: a person is written once however often they use the
// bot, and each later sighting only moves updated_at.
func TestRecordUpsertsOneRow(t *testing.T) {
	conn := pgtest.Connect(t)
	ctx := context.Background()
	const id = int64(987654321)

	read := func() (created, updated time.Time) {
		t.Helper()
		if err := conn.QueryRow(ctx,
			`SELECT created_at, updated_at FROM users WHERE tg_user_id = $1`, id,
		).Scan(&created, &updated); err != nil {
			t.Fatalf("read row: %v", err)
		}
		return created, updated
	}

	if err := users.Record(ctx, conn, id); err != nil {
		t.Fatalf("first record: %v", err)
	}
	created1, updated1 := read()

	if err := users.Record(ctx, conn, id); err != nil {
		t.Fatalf("second record: %v", err)
	}
	created2, updated2 := read()

	var rows int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("got %d rows, want 1 — the repeat inserted instead of updating", rows)
	}
	if !created2.Equal(created1) {
		t.Errorf("created_at moved from %s to %s; it must record the first sighting", created1, created2)
	}
	if !updated2.After(updated1) {
		t.Errorf("updated_at did not advance: %s then %s", updated1, updated2)
	}
}
