//go:build smoke

package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

// This one really talks to Telegram, which is why it sits behind a build tag
// and is never part of `task test`. Run it with `task smoke:cron` after
// setting ADMIN_CHAT_ID, to prove the seam the unit tests can't: real config,
// real Bot API, a message that actually lands in your chat.
//
// It forces the database-connection failure, so the run stops before anything
// could reach the group. Nobody but you sees this.
func TestCronNotifiesAdminOnFailure(t *testing.T) {
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "CRON_SECRET", "ADMIN_CHAT_ID"} {
		if os.Getenv(key) == "" {
			t.Skipf("%s is not set; run through `task smoke:cron` so .env is loaded", key)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Unroutable rather than merely wrong, so the failure is a connection one.
	cfg.DatabaseURL = "postgres://user:pass@127.0.0.1:1/nope"
	run := cronRun{cfg: cfg, bot: telegram.New(cfg.TelegramToken), now: nextReminder(t)}

	req := httptest.NewRequest(http.MethodPost, "/api/cron", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.CronSecret)
	rec := httptest.NewRecorder()

	run.serve(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want %d — the run should have failed", rec.Code, http.StatusInternalServerError)
	}
	t.Log("cron failed as intended; check your private chat for the notice")
}

// nextReminder is ReminderHour on the next weekly reminder day: a run that
// gets as far as the database. Outside ReminderHour the run would skip itself,
// and on a day without duties it would have nothing to connect for.
func nextReminder(t *testing.T) time.Time {
	t.Helper()
	now := time.Now()
	day := berlinTime(t, now.Year(), now.Month(), now.Day(), schedule.ReminderHour, 0)
	for day.Weekday() != weeklyReminderDay {
		day = day.AddDate(0, 0, 1)
	}
	return day
}
