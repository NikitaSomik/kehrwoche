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
		got, err := GetOnDuty(context.Background(), q, DutyTypeToilet1, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Room != "Zimmer 1" {
			t.Errorf("got room %q, want %q", got.Room, "Zimmer 1")
		}
	})

	t.Run("no rows", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
		got, err := GetOnDuty(context.Background(), q, DutyTypeToilet1, now)
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
		_, err := GetOnDuty(context.Background(), q, DutyTypeToilet1, now)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})
}

func TestGetUpcoming(t *testing.T) {
	// Thursday 2026-06-18 → weekly event days are the Fridays 06-19, 06-26, 07-03, 07-10.
	from, _ := time.Parse("2006-01-02", "2026-06-18")
	fri1 := mustDate("2026-06-19")
	fri3 := mustDate("2026-07-03")

	t.Run("fills gaps with empty room", func(t *testing.T) {
		q := fakeQuerier{rows: &fakeRows{data: []fakeRowData{
			{weekStart: fri1, room: "Zimmer 1"},
			{weekStart: fri3, room: "Zimmer 6"},
		}}}

		got, err := GetUpcoming(context.Background(), q, DutyTypeToilet1, from, 4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []Entry{
			{Date: mustDate("2026-06-19"), Room: "Zimmer 1"},
			{Date: mustDate("2026-06-26"), Room: ""},
			{Date: mustDate("2026-07-03"), Room: "Zimmer 6"},
			{Date: mustDate("2026-07-10"), Room: ""},
		}
		assertEntries(t, got, want)
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := fakeQuerier{queryErr: wantErr}
		_, err := GetUpcoming(context.Background(), q, DutyTypeToilet1, from, 4)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})

	t.Run("from inside an already-started window skips to the next one", func(t *testing.T) {
		// Saturday 2026-06-20 falls inside the Fri 06-19 – Sun 06-21 window;
		// the plan must lead with the next Friday, not the one already underway.
		insideWindow := mustDate("2026-06-20")
		fri2 := mustDate("2026-06-26")
		fri4 := mustDate("2026-07-10")

		q := fakeQuerier{rows: &fakeRows{data: []fakeRowData{
			{weekStart: fri2, room: "Zimmer 2"},
			{weekStart: fri4, room: "Zimmer 8"},
		}}}

		got, err := GetUpcoming(context.Background(), q, DutyTypeToilet1, insideWindow, 4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []Entry{
			{Date: mustDate("2026-06-26"), Room: "Zimmer 2"},
			{Date: mustDate("2026-07-03"), Room: ""},
			{Date: mustDate("2026-07-10"), Room: "Zimmer 8"},
			{Date: mustDate("2026-07-17"), Room: ""},
		}
		assertEntries(t, got, want)
	})
}

func TestGetUpcomingLaundry(t *testing.T) {
	// Monday 2026-07-13 → laundry slots alternate Tue/Fri: 07-14, 07-17, 07-21, 07-24.
	// 07-14 (Tue) and 07-17 (Fri) share ISO week 29 — distinct dates must not collide.
	from, _ := time.Parse("2006-01-02", "2026-07-13")
	tue1 := mustDate("2026-07-14")
	fri1 := mustDate("2026-07-17")

	q := fakeQuerier{rows: &fakeRows{data: []fakeRowData{
		{weekStart: tue1, room: "Zimmer 8"},
		{weekStart: fri1, room: "Zimmer 1"},
	}}}

	got, err := GetUpcoming(context.Background(), q, DutyTypeLaundry, from, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []Entry{
		{Date: mustDate("2026-07-14"), Room: "Zimmer 8"},
		{Date: mustDate("2026-07-17"), Room: "Zimmer 1"},
		{Date: mustDate("2026-07-21"), Room: ""},
		{Date: mustDate("2026-07-24"), Room: ""},
	}
	assertEntries(t, got, want)
}

func mustDate(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Date.Equal(want[i].Date) || got[i].Room != want[i].Room {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
