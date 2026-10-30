package handler

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/db"
	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

var weeklyDuties = []schedule.DutyType{
	schedule.DutyTypeFloor, schedule.DutyTypeToilet1,
	schedule.DutyTypeToilet2, schedule.DutyTypeHall,
}

// weeklyReminderDay comes from pkg/schedule so the cron and the /help text
// that announces it are derived from one place.
var weeklyReminderDay = schedule.WeeklyReminderDay()

// dutiesFor picks the duties a reminder covers on a weekday: weekly duties
// the day before their shared event day, laundry on its own event days.
func dutiesFor(weekday time.Weekday) []schedule.DutyType {
	switch {
	case weekday == weeklyReminderDay:
		return weeklyDuties
	case schedule.DutyTypeLaundry.IsEventDay(weekday):
		return []schedule.DutyType{schedule.DutyTypeLaundry}
	default:
		return nil
	}
}

func Cron(w http.ResponseWriter, r *http.Request) {
	cfg := config.Load()

	// Fail-closed: if the secret is not configured, deny all requests.
	// Constant-time comparison to avoid a timing side-channel on the secret.
	got := r.Header.Get("Authorization")
	want := "Bearer " + cfg.CronSecret
	if cfg.CronSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Printf("cron: db connect: %v", err)
		// Deliberately without the error text: DATABASE_URL carries a password
		// and pgx errors can quote the connection string. The log has it.
		reportProblems(ctx, cfg, "Keine Verbindung zur Datenbank")
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			log.Printf("cron: db close: %v", err)
		}
	}()

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Printf("cron: load location: %v", err)
		reportProblems(ctx, cfg, fmt.Sprintf("Zeitzone nicht ladbar: %v", err))
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	now := time.Now().In(loc)

	duties := dutiesFor(now.Weekday())
	if len(duties) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	window := duties[0].Window(now)

	chatID, err := strconv.ParseInt(cfg.ChatID, 10, 64)
	if err != nil {
		log.Printf("cron: invalid CHAT_ID: %v", err)
		reportProblems(ctx, cfg, "CHAT_ID ist keine Zahl — die Erinnerung hat kein Ziel")
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	// One duty type's query failure doesn't drop the others from the reminder.
	var lines []string
	var problems []string
	for _, dutyType := range duties {
		result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
		if err != nil {
			log.Printf("cron: get on duty (%s): %v", dutyType, err)
			problems = append(problems, fmt.Sprintf("%s: Plan nicht lesbar (%v)", dutyType.Label(), err))
			continue
		}
		room := result.Room
		if room == "" {
			room = "—"
		}
		lines = append(lines, fmt.Sprintf("*%s*: %s", dutyType.Label(), room))
	}

	if len(lines) > 0 {
		text := fmt.Sprintf("🏠 *Erinnerung — %s*\n\n%s", window, strings.Join(lines, "\n"))
		if err := telegram.Send(ctx, http.DefaultClient, cfg.TelegramToken, chatID, text); err != nil {
			log.Printf("cron: send: %v", err)
			problems = append(problems, fmt.Sprintf("Erinnerung nicht zustellbar: %v", err))
		}
	}

	problems = append(problems, horizonProblems(ctx, conn, cfg, now)...)

	if len(problems) > 0 {
		reportProblems(ctx, cfg, problems...)
		http.Error(w, "partial failure", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// reportProblems reports a cron run that could not do its job to ADMIN_CHAT_ID,
// as one message however many things went wrong.
//
// Only the cron does this. The webhook answers a person who is standing there
// waiting, so its failures announce themselves; the cron runs unattended, and
// a silent one means nobody is reminded and nobody knows. Runtime logs on the
// Hobby plan are short-lived, so a failure that isn't pushed anywhere is
// effectively invisible.
//
// Best effort by nature: with ADMIN_CHAT_ID unset it does nothing, and when
// the fault is Telegram itself the notice cannot get through either — it is
// still attempted, since a send can fail for reasons specific to the group
// (the bot removed from it, say) while a private message still lands.
func reportProblems(ctx context.Context, cfg config.Config, problems ...string) {
	if cfg.AdminChatID == "" || len(problems) == 0 {
		return
	}
	if err := sendToAdmin(ctx, cfg, adminNotice(problems)); err != nil {
		log.Printf("cron: report problems: %v", err)
	}
}

// sendToAdmin delivers text to ADMIN_CHAT_ID. SendPlain, not Send: error texts
// are arbitrary and would trip Markdown parsing.
func sendToAdmin(ctx context.Context, cfg config.Config, text string) error {
	adminID, err := strconv.ParseInt(cfg.AdminChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid ADMIN_CHAT_ID: %w", err)
	}
	return sendPlain(ctx, http.DefaultClient, cfg.TelegramToken, adminID, text)
}

// sendPlain is telegram.SendPlain, a variable so tests can substitute one
// instead of reaching the Bot API.
var sendPlain = telegram.SendPlain

// adminNotice renders the problems as one message.
func adminNotice(problems []string) string {
	var b strings.Builder
	b.WriteString("⚠️ Der Kehrwoche-Bot hat ein Problem\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "\n• %s", p)
	}
	return b.String()
}

// horizonProblems runs warnHorizon on the weekly reminder day and returns what
// went wrong, if anything. Once a week rather than on every run, and privately:
// only whoever runs cmd/seed can act on it, so the group chat would just be
// noise. The reminder has already gone out by now, so a failure here can't cost
// it; it joins the run's problems because a check that fails silently is the
// shortage it exists to catch.
func horizonProblems(ctx context.Context, conn schedule.Querier, cfg config.Config, now time.Time) []string {
	if now.Weekday() != weeklyReminderDay {
		return nil
	}
	if err := warnHorizon(ctx, conn, cfg, now); err != nil {
		log.Printf("cron: horizon: %v", err)
		return []string{fmt.Sprintf("Putzplan nicht prüfbar: %v", err)}
	}
	return nil
}

// warnHorizon messages the admin when a rolling-horizon duty is about to run
// out of planned weeks. With ADMIN_CHAT_ID unset it does nothing: the notice
// is a maintenance chore, and pushing it to the group instead would tell seven
// people about work only one of them can do.
func warnHorizon(ctx context.Context, conn schedule.Querier, cfg config.Config, now time.Time) error {
	if cfg.AdminChatID == "" {
		return nil
	}
	gaps, err := schedule.HorizonGaps(ctx, conn, schedule.HorizonDuties(), now)
	if err != nil {
		return err
	}
	text := schedule.FormatHorizonWarning(gaps)
	if text == "" {
		return nil
	}
	return sendToAdmin(ctx, cfg, text)
}
