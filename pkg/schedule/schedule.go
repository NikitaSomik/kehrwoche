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

// HorizonWeeks is how far ahead a rolling-horizon duty has to stay planned
// before the bot asks for more rows. It equals PlanWeeks on purpose: the
// warning then arrives exactly when `/*_plan` can no longer show a full
// listing, which is the first moment the shortage is visible to anyone.
const HorizonWeeks = PlanWeeks

// AllDutyTypes lists every duty in a stable order, for callers that need to
// enumerate them (status reports, seeding).
func AllDutyTypes() []DutyType {
	return []DutyType{
		DutyTypeToilet1, DutyTypeToilet2, DutyTypeHall, DutyTypeFloor, DutyTypeLaundry,
	}
}

// IsBlock reports whether a duty is planned one complete block at a time
// rather than on a rolling horizon. Treppenhaus is: the staircase rotates
// between the floors of the house, so our weeks arrive as a block on dates the
// house sets, and the stretches with no rows in between are the other floors'
// turns rather than holes in the plan. Anything that assumes a continuous
// schedule has to leave such a duty out.
func IsBlock(d DutyType) bool {
	return d == DutyTypeHall
}

// HorizonDuties lists the duties planned on a rolling horizon, so running out
// of rows is a real shortage worth reporting.
func HorizonDuties() []DutyType {
	out := make([]DutyType, 0, len(configs))
	for _, d := range AllDutyTypes() {
		if !IsBlock(d) {
			out = append(out, d)
		}
	}
	return out
}

// HorizonGap is a duty whose schedule ends inside HorizonWeeks. Planned is
// false when it has no rows at all, in which case Last is zero.
type HorizonGap struct {
	Duty    DutyType
	Last    time.Time
	Planned bool
}

// FormatHorizonWarning renders the notice sent when the schedule is running
// low. It returns "" for no gaps so the caller can skip sending entirely.
func FormatHorizonWarning(gaps []HorizonGap) string {
	if len(gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("⚠️ *Der Putzplan läuft aus*\n\n")
	for _, g := range gaps {
		if !g.Planned {
			fmt.Fprintf(&b, "*%s*: keine Planung\n", g.Duty.Label())
			continue
		}
		fmt.Fprintf(&b, "*%s*: nur bis %s, %02d.%02d\n",
			g.Duty.Label(), germanWeekday(g.Last.Weekday()), g.Last.Day(), int(g.Last.Month()))
	}
	b.WriteString("\nNachgenerieren: `task seed -- -dry`")
	return b.String()
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

func CleaningWindow(t time.Time) string {
	return windowRange(eventDate(t, weeklyDays, weeklyWindow), weeklyWindow)
}
