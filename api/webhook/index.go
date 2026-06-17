package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "time/tzdata"

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

	cmd := update.Message.Command()
	if cmd != "wer" && cmd != "plan" {
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

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Printf("webhook: load location: %v", err)
		return
	}
	now := time.Now().In(loc)
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := update.Message.Chat.ID

	switch cmd {
	case "wer":
		room, ok, err := schedule.GetOnDuty(ctx, conn, "toilet", now)
		window := schedule.CleaningWindow(now)
		var text string
		if err != nil || !ok {
			text = fmt.Sprintf("❓ %s: keine Planung.", window)
		} else {
			text = fmt.Sprintf("🚽 %s: *%s*", window, room)
		}
		if err := sendTelegram(ctx, token, chatID, text); err != nil {
			log.Printf("webhook: send: %v", err)
		}

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
		if err := sendTelegram(ctx, token, chatID, "📅 *Plan — nächste 4 Wochen:*\n\n"+strings.Join(lines, "\n")); err != nil {
			log.Printf("webhook: send: %v", err)
		}
	}
}

func sendTelegram(ctx context.Context, token string, chatID int64, text string) error {
	body, err := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}