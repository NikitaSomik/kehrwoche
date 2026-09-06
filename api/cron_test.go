package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/config"

	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
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

// stubSendAdmin replaces the Bot API call for the duration of a test and
// records what would have been sent.
func stubSendAdmin(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	prev := sendAdmin
	sendAdmin = func(ctx context.Context, c *http.Client, token string, chatID int64, text string) error {
		sent = append(sent, text)
		return nil
	}
	t.Cleanup(func() { sendAdmin = prev })
	return &sent
}

func TestNotifyAdmin(t *testing.T) {
	t.Run("reaches the admin as one message", func(t *testing.T) {
		sent := stubSendAdmin(t)
		cfg := config.Config{AdminChatID: "456"}

		notifyAdmin(context.Background(), cfg, "Etage: Plan nicht lesbar", "Erinnerung nicht zustellbar")

		if len(*sent) != 1 {
			t.Fatalf("got %d messages, want 1 covering both problems", len(*sent))
		}
		for _, want := range []string{"Etage: Plan nicht lesbar", "Erinnerung nicht zustellbar"} {
			if !strings.Contains((*sent)[0], want) {
				t.Errorf("message is missing %q:\n%s", want, (*sent)[0])
			}
		}
	})

	// The notice is opt-in; without a chat to send it to there is nothing to
	// do, and it must never fall back to the group.
	t.Run("no admin chat, nothing sent", func(t *testing.T) {
		sent := stubSendAdmin(t)

		notifyAdmin(context.Background(), config.Config{}, "something broke")

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})

	t.Run("nothing wrong, nothing sent", func(t *testing.T) {
		sent := stubSendAdmin(t)

		notifyAdmin(context.Background(), config.Config{AdminChatID: "456"})

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})

	t.Run("unparseable admin chat sends nothing", func(t *testing.T) {
		sent := stubSendAdmin(t)
		cfg := config.Config{AdminChatID: "not-a-number"}

		notifyAdmin(context.Background(), cfg, "something broke")

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})
}
