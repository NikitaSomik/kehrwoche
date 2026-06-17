package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitasomusev/kehrwoche/internal/db"
	"github.com/nikitasomusev/kehrwoche/internal/schedule"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Reject requests without the Telegram webhook secret to block fake updates.
	if secret := os.Getenv("WEBHOOK_SECRET"); secret != "" {
		if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	// Always 200 — Telegram retries on any non-200, which causes duplicate messages.
	// Errors are logged but never returned as HTTP errors to Telegram.
	w.WriteHeader(http.StatusOK)

	var update tgbotapi.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		log.Printf("webhook: decode: %v", err)
		return
	}
	if update.Message == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Printf("webhook: db connect: %v", err)
		return
	}
	defer conn.Close(ctx)

	api, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Printf("webhook: bot api: %v", err)
		return
	}

	loc, _ := time.LoadLocation("Europe/Berlin")
	now := time.Now().In(loc)
	chatID := update.Message.Chat.ID

	switch update.Message.Command() {
	case "wer":
		room, ok, err := schedule.GetOnDuty(ctx, conn, "toilet", now)
		window := schedule.CleaningWindow(now)
		var text string
		if err != nil || !ok {
			text = fmt.Sprintf("❓ %s: keine Planung.", window)
		} else {
			text = fmt.Sprintf("🚽 %s: *%s*", window, room)
		}
		sendMsg(api, chatID, text)

	case "plan":
		entries, err := schedule.GetUpcoming(ctx, conn, "toilet", now, 4)
		if err != nil {
			log.Printf("webhook: get upcoming: %v", err)
			return
		}
		lines := make([]string, len(entries))
		for i, e := range entries {
			t := schedule.ParseWeekKey(e.Week)
			window := schedule.CleaningWindow(t)
			room := e.Room
			if room == "" {
				room = "—"
			}
			lines[i] = fmt.Sprintf("%s: %s", window, room)
		}
		sendMsg(api, chatID, "📅 *Plan — nächste 4 Wochen:*\n\n"+strings.Join(lines, "\n"))
	}
}

func sendMsg(api *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := api.Send(msg); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}