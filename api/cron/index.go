package handler

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "time/tzdata"

	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

const dutyType = schedule.DutyTypeToilet

func Handler(w http.ResponseWriter, r *http.Request) {
	// Fail-closed: if the secret is not configured, deny all requests.
	cronSecret := os.Getenv("CRON_SECRET")
	if cronSecret == "" || r.Header.Get("Authorization") != "Bearer "+cronSecret {
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

	window := schedule.CleaningWindow(now)
	result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
	if err != nil {
		log.Printf("cron: get on duty: %v", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	text := result.FormatReminder(window)

	chatID, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		log.Printf("cron: invalid CHAT_ID: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	if err := telegram.Send(ctx, os.Getenv("TELEGRAM_BOT_TOKEN"), chatID, text); err != nil {
		log.Printf("cron: send: %v", err)
		http.Error(w, "send error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
