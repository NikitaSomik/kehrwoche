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

	_ "time/tzdata"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

const dutyType = schedule.DutyTypeToilet

type cmdHandler func(ctx context.Context, conn *pgx.Conn, now time.Time) (string, error)

var commands = map[string]cmdHandler{
	"wer":  handleWer,
	"plan": handlePlan,
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Reject requests without the Telegram webhook secret to block fake updates.
	// Fail-closed: if the secret is not configured, deny all requests.
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" || r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secret {
		w.WriteHeader(http.StatusUnauthorized)
		return
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

	handle, ok := commands[update.Message.Command()]
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Printf("webhook: db connect: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			log.Printf("webhook: db close: %v", err)
		}
	}()

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Printf("webhook: load location: %v", err)
		return
	}

	text, err := handle(ctx, conn, time.Now().In(loc))
	if err != nil {
		log.Printf("webhook: command: %v", err)
		return
	}

	if err := telegram.Send(ctx, os.Getenv("TELEGRAM_BOT_TOKEN"), update.Message.Chat.ID, text); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}

func handleWer(ctx context.Context, conn *pgx.Conn, now time.Time) (string, error) {
	result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
	if err != nil {
		return "", err
	}
	return result.Format(schedule.CleaningWindow(now)), nil
}

func handlePlan(ctx context.Context, conn *pgx.Conn, now time.Time) (string, error) {
	entries, err := schedule.GetUpcoming(ctx, conn, dutyType, now, 4)
	if err != nil {
		return "", err
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		window := schedule.CleaningWindow(schedule.ParseWeekKey(e.Week))
		room := e.Room
		if room == "" {
			room = "—"
		}
		lines[i] = fmt.Sprintf("%s: %s", window, room)
	}
	return "📅 *Plan — nächste 4 Wochen:*\n\n" + strings.Join(lines, "\n"), nil
}
