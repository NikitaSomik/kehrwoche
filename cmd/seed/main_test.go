package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// The rotations are ordered by turn, which is right for generating and wrong
// for a list to check yourself against: 8 comes first in the laundry rotation,
// and nobody looks for it there.
func TestRoomsIn(t *testing.T) {
	got := roomsIn([]schedule.DutyType{schedule.DutyTypeLaundry})
	want := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Two duties that overlap must not list a room twice.
	got = roomsIn([]schedule.DutyType{schedule.DutyTypeToilet1, schedule.DutyTypeToilet2})
	want = []int{1, 2, 3, 4, 5, 6, 7, 8}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseVacant(t *testing.T) {
	t.Run("numbers become a set", func(t *testing.T) {
		got, err := parseVacant("2, 6")
		if err != nil {
			t.Fatalf("parseVacant: %v", err)
		}
		if !got[2] || !got[6] || len(got) != 2 {
			t.Errorf("got %v, want rooms 2 and 6", got)
		}
	})

	t.Run("an empty list is nobody", func(t *testing.T) {
		got, err := parseVacant("")
		if err != nil || len(got) != 0 {
			t.Errorf("got %v, %v; want an empty set", got, err)
		}
	})

	t.Run("a non-number is an error", func(t *testing.T) {
		if _, err := parseVacant("2,zwei"); err == nil {
			t.Error("got nil, want an error")
		}
	})
}

// The flag is the whole answer when it was typed — the list must not reopen a
// question the command line already settled.
func TestChooseDutiesPrefersTheFlag(t *testing.T) {
	a, out := newTestAsker("", true)
	f := cliFlags{duty: "laundry", given: map[string]bool{"duty": true}}

	got, err := chooseDuties(a, f)
	if err != nil {
		t.Fatalf("chooseDuties: %v", err)
	}
	if len(got) != 1 || got[0] != schedule.DutyTypeLaundry {
		t.Errorf("got %v, want just laundry", got)
	}
	if out.String() != "" {
		t.Errorf("printed %q; -duty was given, so there was nothing to ask", out.String())
	}
}

// Answering with enter has to mean what omitting -duty has always meant.
func TestChooseDutiesDefaultsToTheSameSetAsTheFlag(t *testing.T) {
	a, _ := newTestAsker("\n", true)

	got, err := chooseDuties(a, cliFlags{given: map[string]bool{}})
	if err != nil {
		t.Fatalf("chooseDuties: %v", err)
	}
	if !slices.Equal(got, defaultDuties()) {
		t.Errorf("got %v, want %v", got, defaultDuties())
	}
}

// Deselecting everything is not a run with nothing to do — it's a question
// that wasn't answered, and seeding nothing silently would look like success.
func TestChooseDutiesRejectsAnEmptySelection(t *testing.T) {
	// A lone comma clears the pre-selection without being an empty answer,
	// which would mean "keep the defaults".
	a, _ := newTestAsker(",\n", true)

	if _, err := chooseDuties(a, cliFlags{given: map[string]bool{}}); err == nil {
		t.Error("got nil, want an error for a run with no duties")
	}
}

func TestChooseVacantPrefersTheFlag(t *testing.T) {
	a, out := newTestAsker("", true)
	f := cliFlags{vacant: "2,6", given: map[string]bool{"vacant": true}}

	got, err := chooseVacant(a, f, defaultDuties())
	if err != nil {
		t.Fatalf("chooseVacant: %v", err)
	}
	if !got[2] || !got[6] || len(got) != 2 {
		t.Errorf("got %v, want rooms 2 and 6", got)
	}
	if out.String() != "" {
		t.Errorf("printed %q; -vacant was given", out.String())
	}
}

// The list is keyed by room number, so what you type into the fallback is what
// you would have passed to -vacant.
func TestChooseVacantByTypedRoomNumbers(t *testing.T) {
	a, _ := newTestAsker("2,6\n", true)

	got, err := chooseVacant(a, cliFlags{given: map[string]bool{}}, defaultDuties())
	if err != nil {
		t.Fatalf("chooseVacant: %v", err)
	}
	if !got[2] || !got[6] || len(got) != 2 {
		t.Errorf("got %v, want rooms 2 and 6", got)
	}
}

// -regen deletes from -start forward for every duty in the run, and a block
// duty is always seeded with -regen. Combining the two would drop months of
// the in-flat schedule from the staircase block's start date — quietly, since
// the plan would look perfectly ordinary.
func TestCheckBlockDuties(t *testing.T) {
	hall := schedule.DutyTypeHall
	cases := []struct {
		name   string
		duties []schedule.DutyType
		regen  bool
		wantIn string
	}{
		{"the in-flat duties are fine together", defaultDuties(), false, ""},
		{"a block duty alone with -regen is the normal way", []schedule.DutyType{hall}, true, ""},
		{"a block duty alone without -regen is refused", []schedule.DutyType{hall}, false, "-regen -start"},
		{
			"a block duty may not share a run, even with -regen",
			[]schedule.DutyType{hall, schedule.DutyTypeLaundry}, true,
			"on its own",
		},
		{
			"nor without it",
			[]schedule.DutyType{hall, schedule.DutyTypeLaundry}, false,
			"on its own",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBlockDuties(tc.duties, tc.regen)
			if tc.wantIn == "" {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want an error mentioning %q", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error should mention %q: %v", tc.wantIn, err)
			}
		})
	}
}

// The refusal has to name the duties on both sides, or it doesn't say which
// selection to change.
func TestCheckBlockDutiesNamesBothSides(t *testing.T) {
	err := checkBlockDuties([]schedule.DutyType{schedule.DutyTypeHall, schedule.DutyTypeFloor}, true)
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	for _, want := range []string{"hall", "floor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

// The CLI must refuse before it asks the remaining questions or opens a
// connection: a selection that can't be honoured should cost one question,
// not all of them.
func TestRunRefusesAMixedSelectionBeforeAskingOn(t *testing.T) {
	a, out := newTestAsker("", true)
	f := cliFlags{
		duty:  "hall,laundry",
		start: "2026-11-06",
		regen: true,
		given: map[string]bool{"duty": true, "start": true, "regen": true},
	}

	err := run(context.Background(), a, f)
	if err == nil {
		t.Fatal("got nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "on its own") {
		t.Errorf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "Vacant rooms") {
		t.Errorf("the run asked on past the refusal:\n%s", out.String())
	}
}
