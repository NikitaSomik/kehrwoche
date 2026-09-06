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
		notifyAdmin(ctx, cfg, "Keine Verbindung zur Datenbank")
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
		notifyAdmin(ctx, cfg, fmt.Sprintf("Zeitzone nicht ladbar: %v", err))
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
		notifyAdmin(ctx, cfg, "CHAT_ID ist keine Zahl — die Erinnerung hat kein Ziel")
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

	if len(problems) > 0 {
		notifyAdmin(ctx, cfg, problems...)
		http.Error(w, "partial failure", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// notifyAdmin reports a cron run that could not do its job to ADMIN_CHAT_ID,
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
func notifyAdmin(ctx context.Context, cfg config.Config, problems ...string) {
	if cfg.AdminChatID == "" || len(problems) == 0 {
		return
	}
	adminID, err := strconv.ParseInt(cfg.AdminChatID, 10, 64)
	if err != nil {
		log.Printf("cron: invalid ADMIN_CHAT_ID: %v", err)
		return
	}

	// SendPlain, not Send: the error texts are arbitrary and would trip
	// Markdown parsing.
	if err := sendAdmin(ctx, http.DefaultClient, cfg.TelegramToken, adminID, adminNotice(problems)); err != nil {
		log.Printf("cron: notify admin: %v", err)
	}
}

// sendAdmin is the delivery half of notifyAdmin, a variable so tests can
// substitute one instead of reaching the Bot API.
var sendAdmin = telegram.SendPlain

// adminNotice renders the problems as one message.
func adminNotice(problems []string) string {
	var b strings.Builder
	b.WriteString("⚠️ Der Kehrwoche-Bot hat ein Problem\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "\n• %s", p)
	}
	return b.String()
}
