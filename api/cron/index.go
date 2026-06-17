package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitasomusev/kehrwoche/internal/db"
	"github.com/nikitasomusev/kehrwoche/internal/schedule"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	// Vercel passes Authorization: Bearer <CRON_SECRET> for cron invocations.
	// Reject anything that doesn't match so the endpoint can't be triggered externally.
	cronSecret := os.Getenv("CRON_SECRET")
	if cronSecret != "" && r.Header.Get("Authorization") != "Bearer "+cronSecret {
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
	defer conn.Close(ctx)

	loc, _ := time.LoadLocation("Europe/Berlin")
	now := time.Now().In(loc)

	room, ok, err := schedule.GetOnDuty(ctx, conn, "toilet", now)
	if err != nil {
		log.Printf("cron: get on duty: %v", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	window := schedule.CleaningWindow(now)
	var text string
	if !ok {
		text = fmt.Sprintf("🚽 *Toilette — %s*\n\nKeine Planung für diese Woche.", window)
	} else {
		text = fmt.Sprintf("🚽 *Toilette — %s*\n\n%s ist dran!", window, room)
	}

	chatID, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		log.Printf("cron: invalid CHAT_ID: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	api, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Printf("cron: bot api: %v", err)
		http.Error(w, "bot error", http.StatusInternalServerError)
		return
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := api.Send(msg); err != nil {
		log.Printf("cron: send: %v", err)
		http.Error(w, "send error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}