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

func TestPeriods(t *testing.T) {
	if got := periods(26, schedule.DutyTypeFloor); got != 26 {
		t.Errorf("weekly duty: got %d, want 26", got)
	}
	if got := periods(26, schedule.DutyTypeLaundry); got != 52 {
		t.Errorf("laundry (Tue+Fri): got %d, want 52", got)
	}
}

func TestPlanDutyRegenContinuity(t *testing.T) {
	// floor rotation is 1..8; rooms 1 and 6 have moved out.
	rotation := rotations[schedule.DutyTypeFloor]
	active := activeRooms(rotation, map[int]bool{1: true, 6: true})
	// the last assignment before the regen boundary was Zimmer 4.
	tx := fakeTx{row: fakeRow{date: mustDate("2026-08-28"), room: "Zimmer 4"}}

	rows, err := planDuty(context.Background(), tx, schedule.DutyTypeFloor, rotation, active, 4, "2026-09-04", true, false)
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

// generatable walks schedule.AllDutyTypes and keeps what rotations knows, so a
// rotation added for a duty the domain doesn't list would be unreachable —
// seed would reject it by name with no hint why.
func TestGeneratableCoversEveryRotation(t *testing.T) {
	if got, want := len(generatable()), len(rotations); got != want {
		t.Errorf("generatable has %d duties, rotations has %d — one of them is missing from schedule.AllDutyTypes", got, want)
	}
}

func TestSelectedDuties(t *testing.T) {
	t.Run("default leaves hall out", func(t *testing.T) {
		got, err := selectedDuties("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, d := range got {
			if d == schedule.DutyTypeHall {
				t.Fatal("hall must not be seeded by default — its weeks come from the house")
			}
		}
		if len(got) != 4 {
			t.Errorf("got %d duties, want 4", len(got))
		}
	})

	t.Run("hall is selectable by name", func(t *testing.T) {
		got, err := selectedDuties("hall")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != schedule.DutyTypeHall {
			t.Errorf("got %v, want [hall]", got)
		}
	})

	t.Run("unknown duty is rejected", func(t *testing.T) {
		if _, err := selectedDuties("balcony"); err == nil {
			t.Error("expected an error for an unknown duty")
		}
	})
}

// Treppenhaus comes back to our floor as a whole block: one week per occupied
// room, in order, starting again from the first room every time. So the cases
// that matter are the length of the block and the fact that it never carries
// on from where the previous block stopped.
func TestPlanDutyHallBlock(t *testing.T) {
	rotation := rotations[schedule.DutyTypeHall]

	t.Run("a full block, one week per room", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{err: pgx.ErrNoRows}}
		active := activeRooms(rotation, nil)

		rows, err := planDuty(context.Background(), tx, schedule.DutyTypeHall, rotation, active, len(active), "2026-11-06", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(rows) != 8 {
			t.Fatalf("got %d rows, want 8 — one per occupied room", len(rows))
		}
		for i, r := range rows {
			if r.room != i+1 {
				t.Errorf("row %d: got room %d, want %d", i, r.room, i+1)
			}
		}
		if got := rows[0].date.Format(dateLayout); got != "2026-11-06" {
			t.Errorf("first row: got %s, want 2026-11-06", got)
		}
		if got := rows[7].date.Format(dateLayout); got != "2026-12-25" {
			t.Errorf("last row: got %s, want 2026-12-25", got)
		}
	})

	t.Run("the next block starts over at the first room", func(t *testing.T) {
		// Our previous block ended on Zimmer 8; the floors in between have had
		// their turn and it is ours again. The rotation must not carry on.
		tx := fakeTx{row: fakeRow{date: mustDate("2026-09-04"), room: "Zimmer 8"}}
		active := activeRooms(rotation, nil)

		rows, err := planDuty(context.Background(), tx, schedule.DutyTypeHall, rotation, active, len(active), "2026-11-06", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].room != 1 {
			t.Errorf("got room %d, want 1", rows[0].room)
		}
	})

	t.Run("a block that stopped mid-way still starts over", func(t *testing.T) {
		// The guard against a rotation that has silently drifted: whatever the
		// last block ended on, the next one opens with the first room.
		tx := fakeTx{row: fakeRow{date: mustDate("2026-09-04"), room: "Zimmer 3"}}
		active := activeRooms(rotation, nil)

		rows, err := planDuty(context.Background(), tx, schedule.DutyTypeHall, rotation, active, len(active), "2026-11-06", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].room != 1 {
			t.Errorf("got room %d, want 1", rows[0].room)
		}
	})

	t.Run("empty rooms shorten the block", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{err: pgx.ErrNoRows}}
		active := activeRooms(rotation, map[int]bool{1: true, 5: true})

		rows, err := planDuty(context.Background(), tx, schedule.DutyTypeHall, rotation, active, len(active), "2026-11-06", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Six occupied rooms, so six weeks: the floor hands over sooner.
		want := []int{2, 3, 4, 6, 7, 8}
		if len(rows) != len(want) {
			t.Fatalf("got %d rows, want %d", len(rows), len(want))
		}
		for i, r := range rows {
			if r.room != want[i] {
				t.Errorf("row %d: got room %d, want %d", i, r.room, want[i])
			}
		}
	})
}

func TestSeedRejectsHallWithoutRegen(t *testing.T) {
	err := seed(context.Background(), nil, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeHall},
		weeks:  26,
		start:  "2026-11-06",
	})
	if err == nil {
		t.Fatal("expected an error: hall cannot be continued without -regen")
	}
}
