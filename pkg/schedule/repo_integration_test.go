//go:build integration

package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nikitasomusev/kehrwoche/internal/pgtest"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}

func insertRow(t *testing.T, conn *pgx.Conn, duty schedule.DutyType, date, room string) {
	t.Helper()
	if _, err := conn.Exec(context.Background(),
		`INSERT INTO schedules (duty_type, duty_date, room) VALUES ($1, $2, $3)`,
		duty, day(t, date), room,
	); err != nil {
		t.Fatalf("insert %s %s: %v", duty, date, err)
	}
}

func assertEntries(t *testing.T, got, want []schedule.Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Date.Equal(want[i].Date) || got[i].Room != want[i].Room {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestGetOnDuty_Integration(t *testing.T) {
	conn := pgtest.Connect(t)
	ctx := context.Background()

	// Friday 2026-06-19; weekly duties stay current through Sun 06-21.
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-06-19", "Zimmer 3")

	cases := []struct {
		name string
		now  string
		want string
	}{
		{"on the event day", "2026-06-19", "Zimmer 3"},
		{"inside the Fri-Sun window", "2026-06-21", "Zimmer 3"},
		{"past the window rolls to the next, unplanned Friday", "2026-06-23", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schedule.GetOnDuty(ctx, conn, schedule.DutyTypeToilet1, day(t, tc.now))
			if err != nil {
				t.Fatalf("GetOnDuty: %v", err)
			}
			if got.Room != tc.want {
				t.Errorf("Room = %q, want %q", got.Room, tc.want)
			}
		})
	}
}

func TestLastGenerated_Empty(t *testing.T) {
	conn := pgtest.Connect(t)

	_, ok, err := schedule.LastGenerated(context.Background(), conn, schedule.DutyTypeFloor)
	if err != nil {
		t.Fatalf("LastGenerated: %v", err)
	}
	if ok {
		t.Error("ok = true for an empty schedule, want false")
	}
}

func TestLastGenerated_ReturnsMaxForDuty(t *testing.T) {
	conn := pgtest.Connect(t)

	insertRow(t, conn, schedule.DutyTypeFloor, "2026-07-03", "Zimmer 1")
	insertRow(t, conn, schedule.DutyTypeFloor, "2026-07-17", "Zimmer 2")
	insertRow(t, conn, schedule.DutyTypeFloor, "2026-07-10", "Zimmer 3")
	// Another duty with a later date must not leak into the result.
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-12-25", "Zimmer 8")

	got, ok, err := schedule.LastGenerated(context.Background(), conn, schedule.DutyTypeFloor)
	if err != nil {
		t.Fatalf("LastGenerated: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if want := day(t, "2026-07-17"); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestGetUpcoming_FillsGapsInOrder(t *testing.T) {
	conn := pgtest.Connect(t)

	// Thursday 2026-06-18 -> event Fridays 06-19, 06-26, 07-03, 07-10.
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-06-19", "Zimmer 1")
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-07-03", "Zimmer 6")

	got, err := schedule.GetUpcoming(context.Background(), conn, schedule.DutyTypeToilet1, day(t, "2026-06-18"), 4)
	if err != nil {
		t.Fatalf("GetUpcoming: %v", err)
	}
	assertEntries(t, got, []schedule.Entry{
		{Date: day(t, "2026-06-19"), Room: "Zimmer 1"},
		{Date: day(t, "2026-06-26"), Room: ""},
		{Date: day(t, "2026-07-03"), Room: "Zimmer 6"},
		{Date: day(t, "2026-07-10"), Room: ""},
	})
}

func TestGetUpcoming_LaundryTwoDaysSameWeek(t *testing.T) {
	conn := pgtest.Connect(t)

	// Monday 2026-07-13 -> laundry slots Tue/Fri: 07-14, 07-17, 07-21, 07-24.
	// 07-14 and 07-17 fall in the same ISO week and must stay distinct rows.
	insertRow(t, conn, schedule.DutyTypeLaundry, "2026-07-14", "Zimmer 8")
	insertRow(t, conn, schedule.DutyTypeLaundry, "2026-07-17", "Zimmer 1")

	got, err := schedule.GetUpcoming(context.Background(), conn, schedule.DutyTypeLaundry, day(t, "2026-07-13"), 4)
	if err != nil {
		t.Fatalf("GetUpcoming: %v", err)
	}
	assertEntries(t, got, []schedule.Entry{
		{Date: day(t, "2026-07-14"), Room: "Zimmer 8"},
		{Date: day(t, "2026-07-17"), Room: "Zimmer 1"},
		{Date: day(t, "2026-07-21"), Room: ""},
		{Date: day(t, "2026-07-24"), Room: ""},
	})
}

func TestGetUpcoming_FromInsideWindowSkipsToNextEvent(t *testing.T) {
	conn := pgtest.Connect(t)

	// Saturday 2026-06-20 is inside the Fri 06-19 window; the plan must lead
	// with the next Friday, not the one already underway.
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-06-19", "Zimmer 1") // already-started week
	insertRow(t, conn, schedule.DutyTypeToilet1, "2026-06-26", "Zimmer 2")

	got, err := schedule.GetUpcoming(context.Background(), conn, schedule.DutyTypeToilet1, day(t, "2026-06-20"), 2)
	if err != nil {
		t.Fatalf("GetUpcoming: %v", err)
	}
	assertEntries(t, got, []schedule.Entry{
		{Date: day(t, "2026-06-26"), Room: "Zimmer 2"},
		{Date: day(t, "2026-07-03"), Room: ""},
	})
}
