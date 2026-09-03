package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	_ "time/tzdata"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

type cmdHandler func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error)

// botCommand is one slash command: its name (no leading slash), the German
// description shown in Telegram's command menu and /help, and its handler.
type botCommand struct {
	name    string
	desc    string
	handler cmdHandler
}

// botCommands is the single source of truth for the duty commands. The webhook
// lookup map, the /help listing (api/help.go), and the setMyCommands payload
// (cmd/setcommands via MenuCommands) all derive from it. Keep each wer/plan
// pair together and in this order — /help groups by the pair.
var botCommands = []botCommand{
	{"toilette1", "Wer putzt diese Woche die Toilette 1?", wer(schedule.DutyTypeToilet1)},
	{"toilette1_plan", "Plan für die nächsten 4 Wochen (Toilette 1)", plan(schedule.DutyTypeToilet1)},
	{"toilette2", "Wer putzt diese Woche die Toilette 2?", wer(schedule.DutyTypeToilet2)},
	{"toilette2_plan", "Plan für die nächsten 4 Wochen (Toilette 2)", plan(schedule.DutyTypeToilet2)},
	{"treppenhaus", "Wer putzt diese Woche das Treppenhaus?", wer(schedule.DutyTypeHall)},
	{"treppenhaus_plan", "Plan für die nächsten 4 Wochen (Treppenhaus)", plan(schedule.DutyTypeHall)},
	{"etage", "Wer putzt diese Woche die Etage?", wer(schedule.DutyTypeFloor)},
	{"etage_plan", "Plan für die nächsten 4 Wochen (Etage)", plan(schedule.DutyTypeFloor)},
	{"waschkueche", "Wer putzt diese Woche die Waschküche?", wer(schedule.DutyTypeLaundry)},
	{"waschkueche_plan", "Plan für die nächsten 4 Wochen (Waschküche)", plan(schedule.DutyTypeLaundry)},
}

var commands = func() map[string]cmdHandler {
	m := make(map[string]cmdHandler, len(botCommands))
	for _, c := range botCommands {
		m[c.name] = c.handler
	}
	return m
}()

// MenuCommands returns the list for Telegram's setMyCommands in menu order:
// the duty commands, then /help.
func MenuCommands() []telegram.Command {
	out := make([]telegram.Command, 0, len(botCommands)+1)
	for _, c := range botCommands {
		out = append(out, telegram.Command{Command: c.name, Description: c.desc})
	}
	return append(out, telegram.Command{Command: "help", Description: "Alle Befehle anzeigen"})
}

func Webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cfg := config.Load()

	// Reject requests without the Telegram webhook secret to block fake updates.
	// Fail-closed: if the secret is not configured, deny all requests.
	// Constant-time comparison to avoid a timing side-channel on the secret.
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if cfg.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(cfg.WebhookSecret)) != 1 {
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

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := update.Message.Command()

	// /help (any chat) and /start (private only) are static text — no DB needed.
	if reply, ok := staticCommand(cmd, update.Message.Chat.IsPrivate()); ok {
		if err := telegram.SendPlain(ctx, http.DefaultClient, cfg.TelegramToken, update.Message.Chat.ID, reply); err != nil {
			log.Printf("webhook: send static: %v", err)
		}
		return
	}

	handle, ok := commands[cmd]
	if !ok {
		return
	}

	conn, err := db.Connect(ctx, cfg.DatabaseURL)
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

	if err := telegram.Send(ctx, http.DefaultClient, cfg.TelegramToken, update.Message.Chat.ID, text); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}

func wer(dutyType schedule.DutyType) cmdHandler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error) {
		result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
		if err != nil {
			return "", err
		}
		return result.Format(dutyType.Label(), dutyType.Window(now)), nil
	}
}

func plan(dutyType schedule.DutyType) cmdHandler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error) {
		entries, err := schedule.GetUpcoming(ctx, conn, dutyType, now, dutyType.PlanCount())
		if err != nil {
			return "", err
		}
		lines := make([]string, len(entries))
		for i, e := range entries {
			room := e.Room
			if room == "" {
				room = "—"
			}
			lines[i] = fmt.Sprintf("%s: %s", dutyType.Window(e.Date), room)
		}
		return fmt.Sprintf("🗓️ *%s — nächste %d Wochen:*\n\n%s", dutyType.Label(), schedule.PlanWeeks, strings.Join(lines, "\n")), nil
	}
}
