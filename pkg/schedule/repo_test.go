package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row for GetOnDuty tests.
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

// fakeRowData is one row returned by fakeRows.
type fakeRowData struct {
	weekStart time.Time
	room      string
}

// fakeRows implements pgx.Rows for GetUpcoming tests.
type fakeRows struct {
	data []fakeRowData
	pos  int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	if r.pos >= len(r.data) {
		return false
	}
	r.pos++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.pos-1]
	ws, ok := dest[0].(*time.Time)
	if !ok {
		return errors.New("fakeRows: unsupported dest[0] type")
	}
	room, ok := dest[1].(*string)
	if !ok {
		return errors.New("fakeRows: unsupported dest[1] type")
	}
	*ws = row.weekStart
	*room = row.room
	return nil
}

// fakeQuerier implements Querier for repo tests, without a live Postgres connection.
type fakeQuerier struct {
	row      fakeRow
	rows     *fakeRows
	queryErr error
}

func (q fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.row
}

func (q fakeQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	return q.rows, nil
}

func TestGetOnDuty(t *testing.T) {
	now, _ := time.Parse("2006-01-02", "2026-06-18")

	t.Run("room assigned", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{room: "Zimmer 1"}}
		got, err := GetOnDuty(context.Background(), q, DutyTypeToilet, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Room != "Zimmer 1" {
			t.Errorf("got room %q, want %q", got.Room, "Zimmer 1")
		}
	})

	t.Run("no rows", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
		got, err := GetOnDuty(context.Background(), q, DutyTypeToilet, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Room != "" {
			t.Errorf("got room %q, want empty", got.Room)
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := fakeQuerier{row: fakeRow{err: wantErr}}
		_, err := GetOnDuty(context.Background(), q, DutyTypeToilet, now)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})
}

func TestGetUpcoming(t *testing.T) {
	from, _ := time.Parse("2006-01-02", "2026-06-18")
	week1, _ := time.Parse("2006-01-02", "2026-06-15")
	week3, _ := time.Parse("2006-01-02", "2026-06-29")

	t.Run("fills gaps with empty room", func(t *testing.T) {
		q := fakeQuerier{rows: &fakeRows{data: []fakeRowData{
			{weekStart: week1, room: "Zimmer 1"},
			{weekStart: week3, room: "Zimmer 6"},
		}}}

		got, err := GetUpcoming(context.Background(), q, DutyTypeToilet, from, 4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []Entry{
			{Week: "2026-W25", Room: "Zimmer 1"},
			{Week: "2026-W26", Room: ""},
			{Week: "2026-W27", Room: "Zimmer 6"},
			{Week: "2026-W28", Room: ""},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d entries, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := fakeQuerier{queryErr: wantErr}
		_, err := GetUpcoming(context.Background(), q, DutyTypeToilet, from, 4)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})
}
