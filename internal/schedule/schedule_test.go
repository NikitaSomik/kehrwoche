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

func TestExtend(t *testing.T) {
	rooms := []string{"A", "B", "C"}

	t.Run("from empty continues from rooms[0]", func(t *testing.T) {
		entries := Extend(nil, rooms, 6)
		if len(entries) != 6 {
			t.Fatalf("got %d entries, want 6", len(entries))
		}
		for i, e := range entries {
			if want := rooms[i%len(rooms)]; e.Room != want {
				t.Errorf("entry %d: got room %s, want %s", i, e.Room, want)
			}
		}
	})

	t.Run("weeks are consecutive", func(t *testing.T) {
		entries := Extend(nil, rooms, 4)
		for i := 1; i < len(entries); i++ {
			prev := ParseWeekKey(entries[i-1].Week)
			curr := ParseWeekKey(entries[i].Week)
			if diff := curr.Sub(prev); diff != 7*24*time.Hour {
				t.Errorf("entries %d→%d: gap = %v, want 168h", i-1, i, diff)
			}
		}
	})

	t.Run("continues rotation from existing", func(t *testing.T) {
		existing := []Entry{
			{Week: "2026-W20", Room: "B"},
		}
		entries := Extend(existing, rooms, 3)
		if len(entries) != 4 {
			t.Fatalf("got %d entries, want 4", len(entries))
		}
		wantRooms := []string{"B", "C", "A", "B"}
		for i, e := range entries {
			if e.Room != wantRooms[i] {
				t.Errorf("entry %d: got room %s, want %s", i, e.Room, wantRooms[i])
			}
		}
	})
}

func TestOnDuty(t *testing.T) {
	entries := []Entry{
		{Week: "2026-W24", Room: "A"},
		{Week: "2026-W25", Room: "B"},
		{Week: "2026-W26", Room: "C"},
	}

	cases := []struct {
		name     string
		date     string
		wantRoom string
		wantOK   bool
	}{
		{"found", "2026-06-15", "B", true},
		{"not found", "2026-07-13", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now, _ := time.Parse("2006-01-02", tc.date)
			room, ok := OnDuty(entries, now)
			if ok != tc.wantOK || room != tc.wantRoom {
				t.Errorf("got (%q, %v), want (%q, %v)", room, ok, tc.wantRoom, tc.wantOK)
			}
		})
	}
}

func TestUpcoming(t *testing.T) {
	entries := []Entry{
		{Week: "2026-W25", Room: "A"},
		{Week: "2026-W26", Room: "B"},
		{Week: "2026-W27", Room: "C"},
		{Week: "2026-W28", Room: "D"},
	}
	now, _ := time.Parse("2006-01-02", "2026-06-15")

	t.Run("returns n entries", func(t *testing.T) {
		result := Upcoming(entries, now, 4)
		if len(result) != 4 {
			t.Fatalf("got %d entries, want 4", len(result))
		}
		if result[0].Room != "A" {
			t.Errorf("result[0].Room = %q, want A", result[0].Room)
		}
		if result[3].Room != "D" {
			t.Errorf("result[3].Room = %q, want D", result[3].Room)
		}
	})

	t.Run("missing week has empty room", func(t *testing.T) {
		sparse := []Entry{{Week: "2026-W25", Room: "A"}}
		result := Upcoming(sparse, now, 2)
		if result[1].Room != "" {
			t.Errorf("result[1].Room = %q, want empty", result[1].Room)
		}
	})
}
