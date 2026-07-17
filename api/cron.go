package handler

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

var weeklyDuties = []schedule.DutyType{
	schedule.DutyTypeFloor, schedule.DutyTypeToilet1,
	schedule.DutyTypeToilet2, schedule.DutyTypeHall,
}

// weeklyReminderDay is one day ahead of the weekly duties' shared event day,
// derived from pkg/schedule so the reminder can't drift out of sync with the
// cadence it's announcing. Wraps mod 7 so it stays correct even if the event
// day were ever Sunday.
var weeklyReminderDay = time.Weekday((int(weeklyDuties[0].EventWeekdays()[0]) + 6) % 7)

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
	// Fail-closed: if the secret is not configured, deny all requests.
	// Constant-time comparison to avoid a timing side-channel on the secret.
	cronSecret := os.Getenv("CRON_SECRET")
	got := r.Header.Get("Authorization")
	want := "Bearer " + cronSecret
	if cronSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Printf("cron: db connect: %v", err)
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

	chatID, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		log.Printf("cron: invalid CHAT_ID: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	// One duty type's query failure doesn't drop the others from the reminder.
	var lines []string
	var failed bool
	for _, dutyType := range duties {
		result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
		if err != nil {
			log.Printf("cron: get on duty (%s): %v", dutyType, err)
			failed = true
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
		if err := telegram.Send(ctx, http.DefaultClient, os.Getenv("TELEGRAM_BOT_TOKEN"), chatID, text); err != nil {
			log.Printf("cron: send: %v", err)
			failed = true
		}
	}

	if failed {
		http.Error(w, "partial failure", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
