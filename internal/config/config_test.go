package config

import (
	"testing"
)

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("ROOMS", "Zimmer 1,Zimmer 2,Zimmer 3")
	t.Setenv("CHAT_ID", "-1001234567890")
}

func TestLoadFromEnvValid(t *testing.T) {
	t.Run("minimal config defaults", func(t *testing.T) {
		setBaseEnv(t)
		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Token != "test-token" {
			t.Errorf("Token: got %q, want %q", cfg.Token, "test-token")
		}
		if len(cfg.Rooms) != 3 || cfg.Rooms[1] != "Zimmer 2" {
			t.Errorf("Rooms: got %v, want [Zimmer 1 Zimmer 2 Zimmer 3]", cfg.Rooms)
		}
		if cfg.ChatID != -1001234567890 {
			t.Errorf("ChatID: got %d, want -1001234567890", cfg.ChatID)
		}
		if cfg.SchedulePath != "schedule.json" {
			t.Errorf("SchedulePath: got %q, want schedule.json", cfg.SchedulePath)
		}
		if cfg.ScheduleWeeks != defaultScheduleWeeks {
			t.Errorf("ScheduleWeeks: got %d, want %d", cfg.ScheduleWeeks, defaultScheduleWeeks)
		}
		if cfg.Location == nil || cfg.Location.String() != "Europe/Berlin" {
			t.Errorf("Location: got %v, want Europe/Berlin", cfg.Location)
		}
	})

	t.Run("rooms are trimmed", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("ROOMS", " Zimmer 1 , Zimmer 2 ")
		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Rooms[0] != "Zimmer 1" || cfg.Rooms[1] != "Zimmer 2" {
			t.Errorf("Rooms not trimmed: got %v", cfg.Rooms)
		}
	})

	t.Run("custom schedule path", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("SCHEDULE_PATH", "/data/schedule.json")
		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SchedulePath != "/data/schedule.json" {
			t.Errorf("SchedulePath: got %q, want /data/schedule.json", cfg.SchedulePath)
		}
	})

	t.Run("custom schedule weeks", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("SCHEDULE_WEEKS", "16")
		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ScheduleWeeks != 16 {
			t.Errorf("ScheduleWeeks: got %d, want 16", cfg.ScheduleWeeks)
		}
	})
}

func TestLoadFromEnvInvalid(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"missing token", "TELEGRAM_BOT_TOKEN", ""},
		{"missing rooms", "ROOMS", ""},
		{"invalid chat id", "CHAT_ID", "not-a-number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(tc.key, tc.val)
			if _, err := LoadFromEnv(); err == nil {
				t.Errorf("expected error for %s=%q", tc.key, tc.val)
			}
		})
	}
}

func TestLoadFromEnvEmptyRoom(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ROOMS", "Zimmer 1,,Zimmer 3")
	if _, err := LoadFromEnv(); err == nil {
		t.Error("expected error for empty room entry")
	}
}

func TestLoadFromEnvScheduleWeeksFallback(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("SCHEDULE_WEEKS", tc.val)
			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ScheduleWeeks != defaultScheduleWeeks {
				t.Errorf("ScheduleWeeks: got %d, want default %d", cfg.ScheduleWeeks, defaultScheduleWeeks)
			}
		})
	}
}
