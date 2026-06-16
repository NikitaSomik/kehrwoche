package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Entry struct {
	Week string `json:"week"`
	Room string `json:"room"`
}

func Load(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	if err := json.NewDecoder(f).Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func Save(path string, entries []Entry) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// Extend appends `weeks` new entries to existing ones, continuing the rotation.
// If existing is empty, starts from rooms[0] and the current week.
func Extend(existing []Entry, rooms []string, weeks int) []Entry {
	n := len(rooms)
	startIdx := 0
	from := time.Now()

	if len(existing) > 0 {
		last := existing[len(existing)-1]
		for i, r := range rooms {
			if r == last.Room {
				startIdx = (i + 1) % n
				break
			}
		}
		from = ParseWeekKey(last.Week).AddDate(0, 0, 7)
	}

	appended := make([]Entry, weeks)
	for i := range weeks {
		t := from.AddDate(0, 0, i*7)
		appended[i] = Entry{
			Week: WeekKey(t),
			Room: rooms[(startIdx+i)%n],
		}
	}
	return append(existing, appended...)
}

// ParseWeekKey parses "2026-W25" into the Monday of that week.
// Returns zero time if key is malformed.
func ParseWeekKey(key string) time.Time {
	var year, week int
	if n, _ := fmt.Sscanf(key, "%d-W%d", &year, &week); n != 2 {
		return time.Time{}
	}
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.Local)
	mon := jan4.AddDate(0, 0, -(int(jan4.Weekday())-1+7)%7)
	if _, w := mon.ISOWeek(); w != 1 {
		mon = mon.AddDate(0, 0, 7)
	}
	return mon.AddDate(0, 0, (week-1)*7)
}

// OnDuty returns the room on duty for the given week.
func OnDuty(entries []Entry, now time.Time) (string, bool) {
	key := WeekKey(now)
	for _, e := range entries {
		if e.Week == key {
			return e.Room, true
		}
	}
	return "", false
}

// Upcoming returns entries for the next n weeks starting from now.
// Entry with empty Room means no schedule for that week.
func Upcoming(entries []Entry, now time.Time, n int) []Entry {
	index := make(map[string]string, len(entries))
	for _, e := range entries {
		index[e.Week] = e.Room
	}

	result := make([]Entry, n)
	for i := range n {
		t := now.AddDate(0, 0, i*7)
		key := WeekKey(t)
		result[i] = Entry{Week: key, Room: index[key]}
	}
	return result
}

// WeekKey returns the ISO week key for a time, e.g. "2026-W25".
func WeekKey(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
