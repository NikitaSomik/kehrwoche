package schedule

import (
	"fmt"
	"time"
)

// DutyType identifies a cleaning responsibility stored in the DB.
// Add new constants here when new duty areas are introduced.
type DutyType string

const (
	DutyTypeToilet  DutyType = "toilet"
	DutyTypeLaundry DutyType = "laundry"
	DutyTypeHall    DutyType = "hall"
	DutyTypeFloor   DutyType = "floor"
)

// Label returns the German display name used in Telegram messages.
func (d DutyType) Label() string {
	switch d {
	case DutyTypeToilet:
		return "Toilette"
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
	Week string
	Room string
}

// ParseWeekKey parses "2026-W25" into the Monday of that week.
// Returns zero time if key is malformed.
func ParseWeekKey(key string) time.Time {
	var year, week int
	if n, _ := fmt.Sscanf(key, "%d-W%d", &year, &week); n != 2 {
		return time.Time{}
	}
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	mon := jan4.AddDate(0, 0, -(int(jan4.Weekday())-1+7)%7)
	if _, w := mon.ISOWeek(); w != 1 {
		mon = mon.AddDate(0, 0, 7)
	}
	return mon.AddDate(0, 0, (week-1)*7)
}

// OnDutyResult holds the result of an on-duty lookup.
// Room is empty when no schedule entry exists for the week.
type OnDutyResult struct {
	Room string
}

// Format returns a short reply for on-demand commands like /wer.
func (r OnDutyResult) Format(label, window string) string {
	if r.Room == "" {
		return fmt.Sprintf("❓ %s (%s): keine Planung.", label, window)
	}
	return fmt.Sprintf("🏠 %s (%s): *%s*", label, window, r.Room)
}

// WeekKey returns the ISO week key for a time, e.g. "2026-W25".
func WeekKey(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

// CleaningWindow returns the Friday–Sunday date range string for the week containing t,
// e.g. "20.06 – 22.06". Handles month and year rollovers naturally.
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
