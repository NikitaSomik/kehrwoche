package schedule

import (
	"testing"
	"time"
)

func TestWeekKey(t *testing.T) {
	cases := []struct {
		date string
		want string
	}{
		{"2026-06-15", "2026-W25"},
		{"2026-01-01", "2026-W01"},
		{"2025-12-29", "2026-W01"},
		{"2026-12-28", "2026-W53"},
	}
	for _, tc := range cases {
		t.Run(tc.date, func(t *testing.T) {
			d, _ := time.Parse("2006-01-02", tc.date)
			if got := WeekKey(d); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestParseWeekKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"2026-W25", "2026-06-15"},
		{"2026-W01", "2025-12-29"},
		{"2026-W53", "2026-12-28"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := ParseWeekKey(tc.key).Format("2006-01-02")
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestParseWeekKeyMalformed(t *testing.T) {
	cases := []string{"invalid", "", "2026W25", "W25"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			if got := ParseWeekKey(key); !got.IsZero() {
				t.Errorf("ParseWeekKey(%q) = %v, want zero time", key, got)
			}
		})
	}
}

func TestDutyTypeLabel(t *testing.T) {
	cases := []struct {
		dutyType DutyType
		want     string
	}{
		{DutyTypeToilet, "Toilette"},
		{DutyTypeHall, "Treppenhaus"},
		{DutyTypeFloor, "Etage"},
		{DutyTypeLaundry, "Waschküche"},
		{DutyType("unknown"), "unknown"},
	}
	for _, tc := range cases {
		t.Run(string(tc.dutyType), func(t *testing.T) {
			if got := tc.dutyType.Label(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOnDutyResultFormat(t *testing.T) {
	cases := []struct {
		name   string
		result OnDutyResult
		label  string
		window string
		want   string
	}{
		{"room assigned", OnDutyResult{Room: "Zimmer 1"}, "Toilette", "19.06 – 21.06", "🏠 Toilette (19.06 – 21.06): *Zimmer 1*"},
		{"no plan", OnDutyResult{}, "Treppenhaus", "26.06 – 28.06", "❓ Treppenhaus (26.06 – 28.06): keine Planung."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Format(tc.label, tc.window); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCleaningWindow(t *testing.T) {
	cases := []struct {
		name string
		date string
		want string
	}{
		{"thursday mid-month", "2026-06-18", "19.06 – 21.06"},
		{"monday same week", "2026-06-15", "19.06 – 21.06"},
		{"sunday same week", "2026-06-21", "19.06 – 21.06"},
		{"month boundary jan-feb", "2026-01-29", "30.01 – 01.02"},
		{"month boundary mar-apr", "2026-03-26", "27.03 – 29.03"},
		{"year boundary", "2026-12-31", "01.01 – 03.01"},
		{"friday in window", "2026-06-19", "19.06 – 21.06"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := time.Parse("2006-01-02", tc.date)
			if got := CleaningWindow(d); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
