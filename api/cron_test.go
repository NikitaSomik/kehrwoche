package handler

import (
	"net/http"
	"net/http/httptest"
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

func TestCron_Unauthorized(t *testing.T) {
	cases := []struct {
		name       string
		secretEnv  string
		authHeader string
	}{
		{"secret not configured, no header", "", ""},
		{"secret not configured, header sent anyway", "", "Bearer x"},
		{"secret configured, no header", "abc", ""},
		{"secret configured, wrong header", "abc", "Bearer wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRON_SECRET", tc.secretEnv)
			req := httptest.NewRequest(http.MethodPost, "/api/cron", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			Cron(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestCron_ValidSecret_DBError(t *testing.T) {
	// No live DB in this test — db.Connect fails fast on an empty
	// DATABASE_URL, exercising the post-auth error path without a network call.
	t.Setenv("CRON_SECRET", "abc")
	t.Setenv("DATABASE_URL", "")
	req := httptest.NewRequest(http.MethodPost, "/api/cron", nil)
	req.Header.Set("Authorization", "Bearer abc")
	rec := httptest.NewRecorder()

	Cron(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
