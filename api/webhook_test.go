package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nikitasomusev/kehrwoche/internal/schedule"
)

// fakeRow implements pgx.Row for wer() tests.
type fakeRow struct {
	room string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	room, ok := dest[0].(*string)
	if !ok {
		return errors.New("fakeRow: unsupported dest type")
	}
	*room = r.room
	return nil
}

// fakeRows implements pgx.Rows for plan() tests; empty, since these tests
// only need the "no rows" path — GetUpcoming's own logic is covered in
// pkg/schedule/repo_test.go.
type fakeRows struct{}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Next() bool                                   { return false }
func (r *fakeRows) Scan(dest ...any) error                       { return nil }

// fakeQuerier implements schedule.Querier without a live Postgres connection.
type fakeQuerier struct {
	row fakeRow
}

func (q fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.row
}

func (q fakeQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func TestWer(t *testing.T) {
	now, _ := time.Parse("2006-01-02", "2026-06-18")

	t.Run("room assigned", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{room: "Zimmer 4"}}
		got, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "Zimmer 4") {
			t.Errorf("got %q, want it to contain %q", got, "Zimmer 4")
		}
	})

	t.Run("no plan", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
		got, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "keine Planung") {
			t.Errorf("got %q, want it to contain %q", got, "keine Planung")
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := fakeQuerier{row: fakeRow{err: wantErr}}
		_, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})
}

func TestPlan(t *testing.T) {
	now, _ := time.Parse("2006-01-02", "2026-06-18")
	q := fakeQuerier{}

	got, err := plan(schedule.DutyTypeToilet1)(context.Background(), q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Toilette 1") {
		t.Errorf("got %q, want it to contain the duty label", got)
	}
	// ": —" is the empty-room placeholder; the header also contains a bare
	// "—" as a stylistic dash, so counting that alone would overcount.
	if strings.Count(got, ": —") != schedule.DutyTypeToilet1.PlanCount() {
		t.Errorf("got %q, want %d empty-room placeholders", got, schedule.DutyTypeToilet1.PlanCount())
	}
}
