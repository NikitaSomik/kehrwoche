package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	_ "time/tzdata"

	"github.com/nikitasomusev/kehrwoche/pkg/botcmd"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
	"github.com/nikitasomusev/kehrwoche/pkg/users"
)

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

	var update telegram.Update
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
	if reply, ok := botcmd.StaticReply(cmd, update.Message.Chat.IsPrivate()); ok {
		// /start is someone opening the bot in private for the first time, and
		// often the only sighting of a resident who reads the group chat but
		// never types a command in it. Worth the one connection this path
		// otherwise avoids; /help stays free of the database.
		if cmd == botcmd.CmdStart {
			recordSender(ctx, cfg, update.Message)
		}
		if err := telegram.SendPlain(ctx, http.DefaultClient, cfg.TelegramToken, update.Message.Chat.ID, reply); err != nil {
			log.Printf("webhook: send static: %v", err)
		}
		return
	}

	handle, ok := botcmd.Lookup(cmd)
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

	// The connection is open either way, so recording the sender costs nothing
	// beyond the statement itself.
	recordUser(ctx, conn, update.Message)

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

// recordUser stores the sender's Telegram id the first time it is seen.
// Failures are logged and nothing more: knowing who uses the bot must never
// cost somebody an answer.
func recordUser(ctx context.Context, conn users.Execer, msg *telegram.Message) {
	if msg.From == nil {
		return
	}
	if err := users.Record(ctx, conn, msg.From.ID); err != nil {
		log.Printf("webhook: record user: %v", err)
	}
}

// recordSender is recordUser for the paths that hold no connection of their
// own, so it opens and closes one.
func recordSender(ctx context.Context, cfg config.Config, msg *telegram.Message) {
	if msg.From == nil {
		return
	}
	conn, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Printf("webhook: record user: db connect: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			log.Printf("webhook: record user: db close: %v", err)
		}
	}()
	recordUser(ctx, conn, msg)
}
