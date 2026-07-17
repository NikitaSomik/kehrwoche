package handler

import (
	"testing"
	"time"

	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func TestDutiesFor(t *testing.T) {
	cases := []struct {
		weekday time.Weekday
		want    []schedule.DutyType
	}{
		{time.Monday, nil},
		{time.Tuesday, []schedule.DutyType{schedule.DutyTypeLaundry}},
		{time.Wednesday, nil},
		{time.Thursday, weeklyDuties},
		{time.Friday, []schedule.DutyType{schedule.DutyTypeLaundry}},
		{time.Saturday, nil},
		{time.Sunday, nil},
	}
	for _, tc := range cases {
		t.Run(tc.weekday.String(), func(t *testing.T) {
			got := dutiesFor(tc.weekday)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestWeeklyReminderDayIsThursday(t *testing.T) {
	// Weekly duties fire on Friday; the reminder must land on Thursday.
	if weeklyReminderDay != time.Thursday {
		t.Errorf("got %s, want Thursday", weeklyReminderDay)
	}
}
