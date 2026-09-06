package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeExecer struct {
	sql  string
	args []any
	err  error
}

func (f *fakeExecer) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql, f.args = sql, args
	return pgconn.CommandTag{}, f.err
}

func TestRecord(t *testing.T) {
	t.Run("writes the id and leaves a repeat to the database", func(t *testing.T) {
		f := &fakeExecer{}

		if err := Record(context.Background(), f, 987654321); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(f.args) != 1 || f.args[0] != int64(987654321) {
			t.Errorf("got args %v, want the Telegram id alone", f.args)
		}
		// A second sighting must not need a read first, and must move
		// updated_at — see the integration test for both behaviours.
		if !strings.Contains(f.sql, "ON CONFLICT") || !strings.Contains(f.sql, "updated_at = now()") {
			t.Errorf("statement does not upsert through the unique constraint:\n%s", f.sql)
		}
	})

	t.Run("the error is returned", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		f := &fakeExecer{err: wantErr}

		if err := Record(context.Background(), f, 1); !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})
}
