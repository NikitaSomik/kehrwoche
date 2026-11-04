package handler

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
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

// notifier is the part of the Bot API a cron run uses; *telegram.Client
// satisfies it.
type notifier interface {
	Send(ctx context.Context, chatID int64, text string) error
	SendPlain(ctx context.Context, chatID int64, text string) error
}

// cronRun is what one run depends on: the config, the bot and the moment it
// runs at. Cron builds the real ones; tests build their own, so nothing is
// swapped in package state and the tests can run in parallel.
type cronRun struct {
	cfg config.Config
	// cfgErr is what config.Load could not parse. It doesn't stop the run —
	// whatever did parse still works — but the run reports it.
	cfgErr error
	bot    notifier
	now    time.Time
}

func Cron(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	cronRun{cfg: cfg, cfgErr: err, bot: telegram.New(cfg.TelegramToken), now: time.Now()}.serve(w, r)
}

func (c cronRun) serve(w http.ResponseWriter, r *http.Request) {
	// Fail-closed: if the secret is not configured, deny all requests.
	// Constant-time comparison to avoid a timing side-channel on the secret.
	got := r.Header.Get("Authorization")
	want := "Bearer " + c.cfg.CronSecret
	if c.cfg.CronSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var problems []string
	if c.cfgErr != nil {
		problems = append(problems, fmt.Sprintf("Konfiguration fehlerhaft: %v", c.cfgErr))
	}

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Printf("cron: load location: %v", err)
		problems = append(problems, fmt.Sprintf("Zeitzone nicht ladbar: %v", err))
		c.fail(ctx, w, problems)
		return
	}
	now := c.now.In(loc)

	// vercel.json fires at two UTC hours because Vercel Cron knows no time
	// zones; exactly one of them is ReminderHour in Berlin on any given day,
	// summer or winter. The other run ends here, before it opens a connection.
	if now.Hour() != schedule.ReminderHour {
		w.WriteHeader(http.StatusOK)
		return
	}

	if c.cfgErr != nil {
		log.Printf("cron: config: %v", c.cfgErr)
	}
	problems = append(problems, c.remind(ctx, now)...)
	if len(problems) > 0 {
		c.fail(ctx, w, problems)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// fail reports the run's problems and answers 500, so the failure also shows
// in Vercel's cron log.
func (c cronRun) fail(ctx context.Context, w http.ResponseWriter, problems []string) {
	c.reportProblems(ctx, problems...)
	http.Error(w, "cron run failed", http.StatusInternalServerError)
}

// remind sends the day's reminder to the group, then the weekly horizon check,
// and returns what went wrong. One duty type's query failure doesn't drop the
// others from the reminder.
func (c cronRun) remind(ctx context.Context, now time.Time) []string {
	duties := dutiesFor(now.Weekday())
	if len(duties) == 0 {
		return nil
	}
	if c.cfg.ChatID == 0 {
		log.Printf("cron: CHAT_ID is not set")
		return []string{"CHAT_ID fehlt — die Erinnerung hat kein Ziel"}
	}

	conn, err := db.Connect(ctx, c.cfg.DatabaseURL)
	if err != nil {
		log.Printf("cron: db connect: %v", err)
		// Deliberately without the error text: DATABASE_URL carries a password
		// and pgx errors can quote the connection string. The log has it.
		return []string{"Keine Verbindung zur Datenbank"}
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			log.Printf("cron: db close: %v", err)
		}
	}()

	var lines, problems []string
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
		window := duties[0].Window(now)
		text := fmt.Sprintf("🏠 *Erinnerung — %s*\n\n%s", window, strings.Join(lines, "\n"))
		if err := c.bot.Send(ctx, c.cfg.ChatID, text); err != nil {
			log.Printf("cron: send: %v", err)
			problems = append(problems, fmt.Sprintf("Erinnerung nicht zustellbar: %v", err))
		}
	}

	return append(problems, c.horizonProblems(ctx, conn, now)...)
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
func (c cronRun) reportProblems(ctx context.Context, problems ...string) {
	if c.cfg.AdminChatID == 0 || len(problems) == 0 {
		return
	}
	if err := c.sendToAdmin(ctx, adminNotice(problems)); err != nil {
		log.Printf("cron: report problems: %v", err)
	}
}

// sendToAdmin delivers text to ADMIN_CHAT_ID. SendPlain, not Send: error texts
// are arbitrary and would trip Markdown parsing.
func (c cronRun) sendToAdmin(ctx context.Context, text string) error {
	return c.bot.SendPlain(ctx, c.cfg.AdminChatID, text)
}

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
func (c cronRun) horizonProblems(ctx context.Context, conn schedule.Querier, now time.Time) []string {
	if now.Weekday() != weeklyReminderDay {
		return nil
	}
	if err := c.warnHorizon(ctx, conn, now); err != nil {
		log.Printf("cron: horizon: %v", err)
		return []string{fmt.Sprintf("Putzplan nicht prüfbar: %v", err)}
	}
	return nil
}

// warnHorizon messages the admin when a rolling-horizon duty is about to run
// out of planned weeks. With ADMIN_CHAT_ID unset it does nothing: the notice
// is a maintenance chore, and pushing it to the group instead would tell seven
// people about work only one of them can do.
func (c cronRun) warnHorizon(ctx context.Context, conn schedule.Querier, now time.Time) error {
	if c.cfg.AdminChatID == 0 {
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
	return c.sendToAdmin(ctx, text)
}
