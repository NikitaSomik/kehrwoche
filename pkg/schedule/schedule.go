package schedule

import (
	"fmt"
	"strconv"
	"strings"
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

// PlanWeeks is the look-ahead horizon `/*_plan` commands show, in weeks.
const PlanWeeks = 4

func RoomNo(n int) string {
	return fmt.Sprintf("Zimmer %d", n)
}

// ParseRoomNo parses a room label produced by RoomNo back into its number.
func ParseRoomNo(name string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(name, "Zimmer")))
	if err != nil {
		return 0, fmt.Errorf("invalid room label %q", name)
	}
	return n, nil
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

// dutyConfig is a duty's recurrence rule: the weekdays it falls on, and how
// many days starting at the event day that assignment stays current (1 for a
// single-day event, >1 for a duty with a trailing window like Fri–Sun).
type dutyConfig struct {
	days   []time.Weekday
	window int
}

var weeklyDays = []time.Weekday{time.Friday}

const weeklyWindow = 3 // Fri, Sat, Sun

var configs = map[DutyType]dutyConfig{
	DutyTypeToilet1: {days: weeklyDays, window: weeklyWindow},
	DutyTypeToilet2: {days: weeklyDays, window: weeklyWindow},
	DutyTypeHall:    {days: weeklyDays, window: weeklyWindow},
	DutyTypeFloor:   {days: weeklyDays, window: weeklyWindow},
	DutyTypeLaundry: {days: []time.Weekday{time.Tuesday, time.Friday}, window: 1},
}

// EventDate resolves t to the duty day it belongs to: a day within the last
// event's window if we're still in it, otherwise the next event ahead.
func (d DutyType) EventDate(t time.Time) time.Time {
	c := configs[d]
	return eventDate(t, c.days, c.window)
}

// NextEventDate returns the next occurrence strictly after e, regardless of
// window — e is assumed to already be a valid event day.
func (d DutyType) NextEventDate(e time.Time) time.Time {
	return nextWeekdayOnOrAfter(e.AddDate(0, 0, 1), configs[d].days...)
}

// UpcomingEventDate returns the next event day on or after t, ignoring
// whether t already falls inside a previous event's window — used by plan
// listings so they never lead with a day that's already in the past.
func (d DutyType) UpcomingEventDate(t time.Time) time.Time {
	return nextWeekdayOnOrAfter(t, configs[d].days...)
}

func (d DutyType) PlanCount() int {
	return PlanWeeks * len(configs[d].days)
}

// EventWeekdays returns the weekdays d occurs on, so callers (e.g. reminder
// scheduling) derive them from the same cadence data instead of duplicating
// the weekdays as separate literals that can drift out of sync.
func (d DutyType) EventWeekdays() []time.Weekday {
	days := configs[d].days
	out := make([]time.Weekday, len(days))
	copy(out, days)
	return out
}

// IsEventDay reports whether w is one of d's event weekdays. Note this is
// narrower than "duty is in effect on w": a weekly duty's window runs
// Fri–Sun, but only Friday is an event day.
func (d DutyType) IsEventDay(w time.Weekday) bool {
	return hasWeekday(configs[d].days, w)
}

func (d DutyType) Window(t time.Time) string {
	c := configs[d]
	e := d.EventDate(t)
	if c.window > 1 {
		return windowRange(e, c.window)
	}
	return fmt.Sprintf("%s, %02d.%02d", germanWeekday(e.Weekday()), e.Day(), int(e.Month()))
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

func eventDate(t time.Time, days []time.Weekday, window int) time.Time {
	t = dateOnly(t)
	for i := 0; i < window; i++ {
		candidate := t.AddDate(0, 0, -i)
		if hasWeekday(days, candidate.Weekday()) {
			return candidate
		}
	}
	return nextWeekdayOnOrAfter(t, days...)
}

// nextWeekdayOnOrAfter returns the earliest date >= t whose weekday is in days.
func nextWeekdayOnOrAfter(t time.Time, days ...time.Weekday) time.Time {
	t = dateOnly(t)
	for i := 0; i < 7; i++ {
		candidate := t.AddDate(0, 0, i)
		if hasWeekday(days, candidate.Weekday()) {
			return candidate
		}
	}
	panic("nextWeekdayOnOrAfter: no match within a 7-day window")
}

func hasWeekday(days []time.Weekday, w time.Weekday) bool {
	for _, d := range days {
		if d == w {
			return true
		}
	}
	return false
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func windowRange(start time.Time, days int) string {
	end := start.AddDate(0, 0, days-1)
	return fmt.Sprintf("%02d.%02d – %02d.%02d",
		start.Day(), int(start.Month()),
		end.Day(), int(end.Month()),
	)
}

var germanWeekdayNames = map[time.Weekday]string{
	time.Monday:    "Mo",
	time.Tuesday:   "Di",
	time.Wednesday: "Mi",
	time.Thursday:  "Do",
	time.Friday:    "Fr",
	time.Saturday:  "Sa",
	time.Sunday:    "So",
}

func germanWeekday(w time.Weekday) string {
	return germanWeekdayNames[w]
}

func CleaningWindow(t time.Time) string {
	return windowRange(eventDate(t, weeklyDays, weeklyWindow), weeklyWindow)
}
