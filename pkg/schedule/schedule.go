package schedule

import (
	"fmt"
	"time"
)

type DutyType string

const (
	DutyTypeToilet1 DutyType = "toilet1"
	DutyTypeToilet2 DutyType = "toilet2"
	DutyTypeLaundry DutyType = "laundry"
	DutyTypeHall    DutyType = "hall"
	DutyTypeFloor   DutyType = "floor"
)

func RoomNo(n int) string {
	return fmt.Sprintf("Zimmer %d", n)
}

func (d DutyType) Label() string {
	switch d {
	case DutyTypeToilet1:
		return "Toilette 1"
	case DutyTypeToilet2:
		return "Toilette 2"
	case DutyTypeHall:
		return "Treppenhaus"
	case DutyTypeFloor:
		return "Etage"
	case DutyTypeLaundry:
		return "Waschküche"
	default:
		return string(d)
	}
}

type Entry struct {
	Date time.Time
	Room string
}

func (d DutyType) EventDate(t time.Time) time.Time {
	monday := mondayOf(t)
	if d == DutyTypeLaundry {
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if weekday <= 4 { // Mon–Thu → this week's Tuesday
			return monday.AddDate(0, 0, 1)
		}
		return monday.AddDate(0, 0, 4) // Fri–Sun → this week's Friday
	}
	return monday.AddDate(0, 0, 4) // weekly duties → Friday
}

func (d DutyType) NextEventDate(e time.Time) time.Time {
	if d == DutyTypeLaundry {
		if e.Weekday() == time.Tuesday {
			return e.AddDate(0, 0, 3) // Tue → Fri (same week)
		}
		return e.AddDate(0, 0, 4) // Fri → Tue (next week)
	}
	return e.AddDate(0, 0, 7) // weekly → next Friday
}

func (d DutyType) PlanCount() int {
	if d == DutyTypeLaundry {
		return 8
	}
	return 4
}

func (d DutyType) Window(t time.Time) string {
	if d == DutyTypeLaundry {
		e := d.EventDate(t)
		day := "Di"
		if e.Weekday() == time.Friday {
			day = "Fr"
		}
		return fmt.Sprintf("%s, %02d.%02d", day, e.Day(), int(e.Month()))
	}
	return CleaningWindow(t)
}

type OnDutyResult struct {
	Room string
}

func (r OnDutyResult) Format(label, window string) string {
	if r.Room == "" {
		return fmt.Sprintf("❓ %s (%s): keine Planung.", label, window)
	}
	return fmt.Sprintf("🏠 %s (%s): *%s*", label, window, r.Room)
}

func mondayOf(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	d := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

func CleaningWindow(t time.Time) string {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := t.AddDate(0, 0, -(weekday - 1))
	friday := monday.AddDate(0, 0, 4)
	sunday := monday.AddDate(0, 0, 6)
	return fmt.Sprintf("%02d.%02d – %02d.%02d",
		friday.Day(), int(friday.Month()),
		sunday.Day(), int(sunday.Month()),
	)
}
