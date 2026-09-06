package schedule

import (
	"fmt"
	"slices"
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

// AllDutyTypes lists every duty in a stable order, for callers that need to
// enumerate them (status reports, seeding).
func AllDutyTypes() []DutyType {
	return []DutyType{
		DutyTypeToilet1, DutyTypeToilet2, DutyTypeHall, DutyTypeFloor, DutyTypeLaundry,
	}
}

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

// ReminderHour is the local hour the reminder cron fires at. It mirrors
// vercel.json, which Go can't read at runtime, so the two are kept in step by
// hand. The month-based DST split there drifts an hour in the last week of
// March and of October.
const ReminderHour = 11

// WeeklyReminderDay is the day the weekly duties are announced on: one day
// ahead of their shared event day. Wraps mod 7 so it stays correct even if the
// event day were ever Sunday.
func WeeklyReminderDay() time.Weekday {
	return time.Weekday((int(weeklyDays[0]) + 6) % 7)
}

// ReminderWeekdays lists every weekday the bot announces on, Monday first:
// the weekly duties are announced the day before their shared event day, and
// anything on its own cadence (Waschküche) on its event days themselves.
// Derived from configs so /help and the cron can't tell different stories.
func ReminderWeekdays() []time.Weekday {
	seen := make(map[time.Weekday]bool, len(configs))
	for _, d := range AllDutyTypes() {
		if slices.Equal(configs[d].days, weeklyDays) {
			seen[WeeklyReminderDay()] = true
			continue
		}
		for _, w := range configs[d].days {
			seen[w] = true
		}
	}

	week := []time.Weekday{
		time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
		time.Friday, time.Saturday, time.Sunday,
	}
	out := make([]time.Weekday, 0, len(seen))
	for _, w := range week {
		if seen[w] {
			out = append(out, w)
		}
	}
	return out
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
	return slices.Contains(configs[d].days, w)
}

// WindowDays is how many days a single assignment stays current, counting
// from its event day (3 for weekly Fri–Sun duties, 1 for Waschküche).
func (d DutyType) WindowDays() int {
	return configs[d].window
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
	for i := range window {
		candidate := t.AddDate(0, 0, -i)
		if slices.Contains(days, candidate.Weekday()) {
			return candidate
		}
	}
	return nextWeekdayOnOrAfter(t, days...)
}

// nextWeekdayOnOrAfter returns the earliest date >= t whose weekday is in days.
func nextWeekdayOnOrAfter(t time.Time, days ...time.Weekday) time.Time {
	t = dateOnly(t)
	for i := range 7 {
		candidate := t.AddDate(0, 0, i)
		if slices.Contains(days, candidate.Weekday()) {
			return candidate
		}
	}
	panic("nextWeekdayOnOrAfter: no match within a 7-day window")
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

var germanWeekdayAdverbs = map[time.Weekday]string{
	time.Monday:    "montags",
	time.Tuesday:   "dienstags",
	time.Wednesday: "mittwochs",
	time.Thursday:  "donnerstags",
	time.Friday:    "freitags",
	time.Saturday:  "samstags",
	time.Sunday:    "sonntags",
}

// GermanWeekdayAdverb returns the German adverb for "every <weekday>", for
// prose rather than the Mo..So abbreviations used in date listings.
func GermanWeekdayAdverb(w time.Weekday) string {
	return germanWeekdayAdverbs[w]
}

func CleaningWindow(t time.Time) string {
	return windowRange(eventDate(t, weeklyDays, weeklyWindow), weeklyWindow)
}
