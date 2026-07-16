package schedule

import (
	"testing"
	"time"
)

func TestDutyTypeLabel(t *testing.T) {
	cases := []struct {
		dutyType DutyType
		want     string
	}{
		{DutyTypeToilet1, "Toilette 1"},
		{DutyTypeToilet2, "Toilette 2"},
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

func TestEventDate(t *testing.T) {
	cases := []struct {
		name string
		duty DutyType
		date string
		want string
	}{
		// weekly duties resolve to the Friday of the ISO week, from any weekday
		{"weekly monday", DutyTypeToilet1, "2026-06-15", "2026-06-19"},
		{"weekly thursday", DutyTypeFloor, "2026-06-18", "2026-06-19"},
		{"weekly friday", DutyTypeHall, "2026-06-19", "2026-06-19"},
		{"weekly sunday", DutyTypeToilet2, "2026-06-21", "2026-06-19"},
		// laundry: Tuesday slot for Mon–Thu, Friday slot for Fri–Sun
		{"laundry monday", DutyTypeLaundry, "2026-07-13", "2026-07-14"},
		{"laundry tuesday", DutyTypeLaundry, "2026-07-14", "2026-07-14"},
		{"laundry thursday", DutyTypeLaundry, "2026-07-16", "2026-07-14"},
		{"laundry friday", DutyTypeLaundry, "2026-07-17", "2026-07-17"},
		{"laundry sunday", DutyTypeLaundry, "2026-07-19", "2026-07-17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := time.Parse("2006-01-02", tc.date)
			if got := tc.duty.EventDate(d).Format("2006-01-02"); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNextEventDate(t *testing.T) {
	// weekly steps by 7 days; laundry alternates Tue→Fri (+3) then Fri→Tue (+4)
	weeklyFrom, _ := time.Parse("2006-01-02", "2026-06-19")
	if got := DutyTypeToilet1.NextEventDate(weeklyFrom).Format("2006-01-02"); got != "2026-06-26" {
		t.Errorf("weekly next: got %s, want 2026-06-26", got)
	}
	tue, _ := time.Parse("2006-01-02", "2026-07-14")
	if got := DutyTypeLaundry.NextEventDate(tue).Format("2006-01-02"); got != "2026-07-17" {
		t.Errorf("laundry Tue→Fri: got %s, want 2026-07-17", got)
	}
	fri, _ := time.Parse("2006-01-02", "2026-07-17")
	if got := DutyTypeLaundry.NextEventDate(fri).Format("2006-01-02"); got != "2026-07-21" {
		t.Errorf("laundry Fri→Tue: got %s, want 2026-07-21", got)
	}
}

func TestDutyTypeWindow(t *testing.T) {
	cases := []struct {
		name string
		duty DutyType
		date string
		want string
	}{
		{"weekly range", DutyTypeToilet1, "2026-06-18", "19.06 – 21.06"},
		{"laundry tuesday", DutyTypeLaundry, "2026-07-15", "Di, 14.07"},
		{"laundry friday", DutyTypeLaundry, "2026-07-17", "Fr, 17.07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := time.Parse("2006-01-02", tc.date)
			if got := tc.duty.Window(d); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDutyTypePlanCount(t *testing.T) {
	if got := DutyTypeToilet1.PlanCount(); got != 4 {
		t.Errorf("weekly: got %d, want 4", got)
	}
	if got := DutyTypeLaundry.PlanCount(); got != 8 {
		t.Errorf("laundry: got %d, want 8", got)
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
