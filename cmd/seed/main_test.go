package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

// fakeRow implements pgx.Row for lastRow tests. lastRow scans (duty_date, room).
type fakeRow struct {
	date time.Time
	room string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*time.Time) = r.date
	*dest[1].(*string) = r.room
	return nil
}

func mustDate(s string) time.Time {
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return d
}

// fakeTx implements txQuerier without a real DB transaction.
type fakeTx struct{ row fakeRow }

func (f fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return f.row }
func (f fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestLastRow(t *testing.T) {
	t.Run("parses the room from the label", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{room: "Zimmer 3"}}
		got, ok, err := lastRow(context.Background(), tx, "toilet1", time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok || got.room != 3 {
			t.Errorf("got %+v, %v, want room 3, true", got, ok)
		}
	})

	t.Run("no rows yet", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{err: pgx.ErrNoRows}}
		_, ok, err := lastRow(context.Background(), tx, "toilet1", time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("got ok=true, want false")
		}
	})

	t.Run("unparseable room label", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{room: "not a room"}}
		_, _, err := lastRow(context.Background(), tx, "toilet1", time.Time{})
		if err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		tx := fakeTx{row: fakeRow{err: wantErr}}
		_, _, err := lastRow(context.Background(), tx, "toilet1", time.Time{})
		if !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})
}

func TestActiveRooms(t *testing.T) {
	got := activeRooms([]int{1, 2, 3, 4}, map[int]bool{2: true})
	want := []int{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestNextActiveIndex(t *testing.T) {
	full := []int{1, 2, 3, 4, 5, 6, 7, 8}

	t.Run("no vacancies: just the next room", func(t *testing.T) {
		if got := nextActiveIndex(full, full, 4); got != 4 { // room 5 is at index 4
			t.Errorf("got %d, want 4", got)
		}
	})

	t.Run("wraps around", func(t *testing.T) {
		if got := nextActiveIndex(full, full, 8); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("skips vacant rooms after lastRoom", func(t *testing.T) {
		active := []int{2, 3, 4, 6, 7, 8} // 1 and 5 moved out
		// after room 4, room 5 is gone, so the next is room 6 -> active index 3
		if got := nextActiveIndex(full, active, 4); got != 3 {
			t.Errorf("got %d, want 3 (room 6)", got)
		}
	})

	t.Run("lastRoom itself vacant: continues from its slot", func(t *testing.T) {
		active := []int{2, 3, 4, 6, 7, 8}
		// room 5 is the one that left; the next in rotation is room 6 -> index 3
		if got := nextActiveIndex(full, active, 5); got != 3 {
			t.Errorf("got %d, want 3 (room 6)", got)
		}
	})

	t.Run("lastRoom not in rotation: falls back to 0", func(t *testing.T) {
		if got := nextActiveIndex(full, full, 99); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

func TestPlanDutyRegenContinuity(t *testing.T) {
	// floor rotation is 1..8; rooms 1 and 6 have moved out.
	rotation := rotations[schedule.DutyTypeFloor]
	active := activeRooms(rotation, map[int]bool{1: true, 6: true})
	// the last assignment before the regen boundary was Zimmer 4.
	tx := fakeTx{row: fakeRow{date: mustDate("2026-08-28"), room: "Zimmer 4"}}

	rows, err := planDuty(context.Background(), tx, schedule.DutyTypeFloor, rotation, active, 4, "2026-09-04", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After Zimmer 4 the cycle continues 5, 7, 8, 2 — rooms 1 and 6 are simply
	// skipped, not a reset back to the first room.
	want := []int{5, 7, 8, 2}
	for i, r := range rows {
		if r.room != want[i] {
			t.Errorf("row %d: got room %d, want %d", i, r.room, want[i])
		}
	}
	if !rows[0].date.Equal(mustDate("2026-09-04")) {
		t.Errorf("first row date: got %s, want 2026-09-04", rows[0].date.Format("2006-01-02"))
	}
}
