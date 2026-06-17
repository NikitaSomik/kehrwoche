package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "time/tzdata"

	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func Handler(w http.ResponseWriter, r *http.Request) {
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

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Printf("cron: load location: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
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
		text = fmt.Sprintf("🏠 *Toilette — %s*\n\nKeine Planung für diese Woche.", window)
	} else {
		text = fmt.Sprintf("🏠 *Toilette — %s*\n\nErinnerung: *%s* ist diese Woche für die Toilette zuständig.", window, room)
	}

	chatID, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		log.Printf("cron: invalid CHAT_ID: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	if err := sendTelegram(ctx, os.Getenv("TELEGRAM_BOT_TOKEN"), chatID, text); err != nil {
		log.Printf("cron: send: %v", err)
		http.Error(w, "send error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
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
