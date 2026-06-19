package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func TestNextThursday(t *testing.T) {
	loc := time.UTC

	cases := []struct {
		name string
		from string
		want string
	}{
		{"monday", "2026-06-15 08:00:00", "2026-06-18 09:00:00"},
		{"wednesday", "2026-06-17 23:59:59", "2026-06-18 09:00:00"},
		{"thursday before 9", "2026-06-18 08:59:59", "2026-06-25 09:00:00"},
		{"thursday at 9", "2026-06-18 09:00:00", "2026-06-25 09:00:00"},
		{"thursday after 9", "2026-06-18 09:00:01", "2026-06-25 09:00:00"},
		{"friday", "2026-06-19 10:00:00", "2026-06-25 09:00:00"},
		{"sunday", "2026-06-21 20:00:00", "2026-06-25 09:00:00"},
		{"year boundary", "2026-12-31 09:00:01", "2027-01-07 09:00:00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.from, loc)
			want, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.want, loc)
			got := nextThursday(from)
			if !got.Equal(want) {
				t.Errorf("nextThursday(%s) = %s, want %s", tc.from, got.Format("2006-01-02 15:04:05"), tc.want)
			}
		})
	}
}

func makeCfg(path string) *config.Config {
	return &config.Config{
		Rooms:         []string{"A", "B", "C"},
		ScheduleWeeks: 4,
		SchedulePath:  path,
	}
}

func TestLoadOrGenerate(t *testing.T) {
	t.Run("no file generates fresh schedule", func(t *testing.T) {
		cfg := makeCfg(filepath.Join(t.TempDir(), "schedule.json"))

		entries, err := loadOrGenerate(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != cfg.ScheduleWeeks {
			t.Errorf("got %d entries, want %d", len(entries), cfg.ScheduleWeeks)
		}
		if _, err := os.Stat(cfg.SchedulePath); err != nil {
			t.Errorf("schedule file was not created: %v", err)
		}
	})

	t.Run("existing file gets extended", func(t *testing.T) {
		cfg := makeCfg(filepath.Join(t.TempDir(), "schedule.json"))

		initial := []schedule.Entry{
			{Week: "2026-W20", Room: "A"},
			{Week: "2026-W21", Room: "B"},
		}
		if err := schedule.Save(cfg.SchedulePath, initial); err != nil {
			t.Fatalf("setup: save schedule: %v", err)
		}

		entries, err := loadOrGenerate(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := len(initial) + cfg.ScheduleWeeks
		if len(entries) != want {
			t.Errorf("got %d entries, want %d", len(entries), want)
		}
		if entries[0] != initial[0] || entries[1] != initial[1] {
			t.Error("existing entries were modified")
		}
	})

	t.Run("malformed file returns error", func(t *testing.T) {
		cfg := makeCfg(filepath.Join(t.TempDir(), "schedule.json"))

		if err := os.WriteFile(cfg.SchedulePath, []byte("not valid json"), 0600); err != nil {
			t.Fatalf("setup: write file: %v", err)
		}

		if _, err := loadOrGenerate(cfg); err == nil {
			t.Error("expected error for malformed schedule file")
		}
	})

	t.Run("new room appears in extended entries", func(t *testing.T) {
		dir := t.TempDir()

		// initial generation with two rooms
		initial, err := loadOrGenerate(&config.Config{
			Rooms:         []string{"A", "B"},
			ScheduleWeeks: 2,
			SchedulePath:  filepath.Join(dir, "schedule.json"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(initial) != 2 {
			t.Fatalf("got %d initial entries, want 2", len(initial))
		}

		// add room C and extend
		cfg := makeCfg(filepath.Join(dir, "schedule.json")) // rooms: A, B, C
		entries, err := loadOrGenerate(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, e := range entries {
			if e.Room == "C" {
				found = true
				break
			}
		}
		if !found {
			t.Error("new room C not found in extended schedule")
		}
	})
}