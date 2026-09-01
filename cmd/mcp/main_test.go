package main

import (
	"testing"
	"time"

	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func TestParseDuty(t *testing.T) {
	t.Run("known key", func(t *testing.T) {
		got, err := parseDuty("floor")
		if err != nil || got != schedule.DutyTypeFloor {
			t.Fatalf("got %q, %v; want floor, nil", got, err)
		}
	})

	t.Run("unknown key is an error", func(t *testing.T) {
		if _, err := parseDuty("kitchen"); err == nil {
			t.Error("expected an error for an unknown duty")
		}
	})
}

func TestSelectDuties(t *testing.T) {
	t.Run("empty arg means every duty", func(t *testing.T) {
		got, err := selectDuties("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(schedule.AllDutyTypes()) {
			t.Errorf("got %d duties, want %d", len(got), len(schedule.AllDutyTypes()))
		}
	})

	t.Run("a key narrows to one", func(t *testing.T) {
		got, err := selectDuties("floor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != schedule.DutyTypeFloor {
			t.Errorf("got %v, want [floor]", got)
		}
	})

	t.Run("an unknown key is an error", func(t *testing.T) {
		if _, err := selectDuties("kitchen"); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestResolveDate(t *testing.T) {
	loc := time.UTC

	t.Run("empty means now", func(t *testing.T) {
		got, err := resolveDate("", loc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if time.Since(got) > time.Minute {
			t.Errorf("got %v, want roughly now", got)
		}
	})

	t.Run("parses YYYY-MM-DD", func(t *testing.T) {
		got, err := resolveDate("2026-08-28", loc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Year() != 2026 || got.Month() != time.August || got.Day() != 28 {
			t.Errorf("got %v, want 2026-08-28", got)
		}
	})

	t.Run("rejects a bad date", func(t *testing.T) {
		if _, err := resolveDate("28.08.2026", loc); err == nil {
			t.Error("expected an error for a non-ISO date")
		}
	})
}
