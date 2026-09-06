package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nikitasomusev/kehrwoche/pkg/config"
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

// horizonQuerier answers every max(duty_date) with the same date — enough for
// warnHorizon, which only cares whether a gap exists at all. It counts calls so
// a test can assert the DB is never touched.
type horizonQuerier struct {
	t     *testing.T
	last  *time.Time
	err   error
	calls int
}

func (q *horizonQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.calls++
	return horizonRow{last: q.last, err: q.err}
}

func (q *horizonQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.t.Helper()
	q.t.Error("warnHorizon must not run a multi-row query")
	return nil, errors.New("horizonQuerier: Query not used")
}

type horizonRow struct {
	last *time.Time
	err  error
}

func (r horizonRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(**time.Time) = r.last
	return nil
}

// Every case here stops before telegram.Send: the send path talks to the real
// API over http.DefaultClient, so it stays out of the unit tests.
func TestWarnHorizon(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	far := now.AddDate(0, 0, 100)
	near := now.AddDate(0, 0, 7)

	t.Run("no admin chat, no query", func(t *testing.T) {
		// The notice is off, so it must not cost a round-trip either.
		q := &horizonQuerier{t: t, last: &near}

		if err := warnHorizon(context.Background(), q, config.Config{}, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.calls != 0 {
			t.Errorf("queried the DB %d times, want 0", q.calls)
		}
	})

	t.Run("nothing running out, nothing sent", func(t *testing.T) {
		q := &horizonQuerier{t: t, last: &far}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.calls != len(schedule.HorizonDuties()) {
			t.Errorf("got %d queries, want one per horizon duty (%d)", q.calls, len(schedule.HorizonDuties()))
		}
	})

	t.Run("unparseable admin chat is an error", func(t *testing.T) {
		q := &horizonQuerier{t: t, last: &near}
		cfg := config.Config{AdminChatID: "not-a-number"}

		err := warnHorizon(context.Background(), q, cfg, now)
		if err == nil {
			t.Fatal("got nil, want an error naming ADMIN_CHAT_ID")
		}
		if !strings.Contains(err.Error(), "ADMIN_CHAT_ID") {
			t.Errorf("got %v, want it to name ADMIN_CHAT_ID", err)
		}
	})

	t.Run("query error is propagated", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := &horizonQuerier{t: t, err: wantErr}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})
}
