//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nikitasomusev/kehrwoche/internal/pgtest"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

type row struct {
	date time.Time
	room string
}

func rowsFor(t *testing.T, conn *pgx.Conn, duty schedule.DutyType) []row {
	t.Helper()
	rs, err := conn.Query(context.Background(),
		`SELECT duty_date, room FROM schedules WHERE duty_type = $1 ORDER BY duty_date`, duty)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rs.Close()

	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.date, &r.room); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func insertRow(t *testing.T, conn *pgx.Conn, duty schedule.DutyType, date, room string) {
	t.Helper()
	if _, err := conn.Exec(context.Background(),
		`INSERT INTO schedules (duty_type, duty_date, room) VALUES ($1, $2, $3)`,
		duty, mustDate(date), room,
	); err != nil {
		t.Fatalf("insert %s %s: %v", duty, date, err)
	}
}

func assertRows(t *testing.T, got, want []row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].date.Equal(want[i].date) || got[i].room != want[i].room {
			t.Errorf("row %d: got {%s %s}, want {%s %s}", i,
				got[i].date.Format(dateLayout), got[i].room,
				want[i].date.Format(dateLayout), want[i].room)
		}
	}
}

func roomOn(rows []row, date string) string {
	for _, r := range rows {
		if r.date.Format(dateLayout) == date {
			return r.room
		}
	}
	return ""
}

func TestSeed_FreshStart(t *testing.T) {
	conn := pgtest.Connect(t)

	err := seed(context.Background(), conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeToilet1},
		weeks:  3,
		start:  "2026-06-19", // Friday
		vacant: map[int]bool{},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// toilet1 rotation is {4, 3, 7}.
	assertRows(t, rowsFor(t, conn, schedule.DutyTypeToilet1), []row{
		{mustDate("2026-06-19"), "Zimmer 4"},
		{mustDate("2026-06-26"), "Zimmer 3"},
		{mustDate("2026-07-03"), "Zimmer 7"},
	})
}

func TestSeed_AppendContinuesRotation(t *testing.T) {
	conn := pgtest.Connect(t)

	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-06-19", "Zimmer 4")

	// No start: append mode picks up from the last stored row.
	err := seed(context.Background(), conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeToilet1},
		weeks:  2,
		vacant: map[int]bool{},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	assertRows(t, rowsFor(t, conn, schedule.DutyTypeToilet1), []row{
		{mustDate("2026-06-19"), "Zimmer 4"},
		{mustDate("2026-06-26"), "Zimmer 3"},
		{mustDate("2026-07-03"), "Zimmer 7"},
	})
}

func TestSeed_RegenAfterMoveOut(t *testing.T) {
	conn := pgtest.Connect(t)
	ctx := context.Background()

	// Seed floor (rotation 1..8) for 8 weeks from Fri 2026-06-19.
	if err := seed(ctx, conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeFloor},
		weeks:  8,
		start:  "2026-06-19",
		vacant: map[int]bool{},
	}); err != nil {
		t.Fatalf("initial seed: %v", err)
	}

	// Room 3 moves out; regenerate from week 4 (2026-07-10).
	if err := seed(ctx, conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeFloor},
		weeks:  8,
		start:  "2026-07-10",
		vacant: map[int]bool{3: true},
		regen:  true,
	}); err != nil {
		t.Fatalf("regen seed: %v", err)
	}

	got := rowsFor(t, conn, schedule.DutyTypeFloor)

	// Rows before the regen boundary are left exactly as they were.
	assertRows(t, got[:3], []row{
		{mustDate("2026-06-19"), "Zimmer 1"},
		{mustDate("2026-06-26"), "Zimmer 2"},
		{mustDate("2026-07-03"), "Zimmer 3"},
	})

	// The rotation continues from Zimmer 3's successor (4) — not a reset — and
	// the vacant room 3 never appears again.
	if got := roomOn(got, "2026-07-10"); got != "Zimmer 4" {
		t.Errorf("2026-07-10 room = %q, want Zimmer 4", got)
	}
	for _, r := range got {
		if !r.date.Before(mustDate("2026-07-10")) && r.room == "Zimmer 3" {
			t.Errorf("vacant room 3 assigned on %s", r.date.Format(dateLayout))
		}
	}
}

func TestSeed_DryRunWritesNothing(t *testing.T) {
	conn := pgtest.Connect(t)

	err := seed(context.Background(), conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeToilet1},
		weeks:  3,
		start:  "2026-06-19",
		vacant: map[int]bool{},
		dry:    true,
	})
	if err != nil {
		t.Fatalf("seed -dry: %v", err)
	}

	if got := rowsFor(t, conn, schedule.DutyTypeToilet1); len(got) != 0 {
		t.Errorf("dry run wrote %d rows, want 0", len(got))
	}
}

func TestSeed_LaundryTwiceWeekly(t *testing.T) {
	conn := pgtest.Connect(t)

	err := seed(context.Background(), conn, seedParams{
		duties: []schedule.DutyType{schedule.DutyTypeLaundry},
		weeks:  2,
		start:  "2026-06-16", // Tuesday
		vacant: map[int]bool{},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// laundry rotation is {8, 1, 2, 3, ...}; slots alternate Tue/Fri.
	assertRows(t, rowsFor(t, conn, schedule.DutyTypeLaundry), []row{
		{mustDate("2026-06-16"), "Zimmer 8"},
		{mustDate("2026-06-19"), "Zimmer 1"},
		{mustDate("2026-06-23"), "Zimmer 2"},
		{mustDate("2026-06-26"), "Zimmer 3"},
	})
}
