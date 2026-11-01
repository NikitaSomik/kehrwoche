package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	_ "time/tzdata"

	"github.com/NikitaSomik/kehrwoche/pkg/botcmd"
	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/db"
	"github.com/NikitaSomik/kehrwoche/pkg/onboard"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
	"github.com/NikitaSomik/kehrwoche/pkg/users"
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

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	bot := telegram.Client{HTTP: http.DefaultClient, Token: cfg.TelegramToken}
	switch {
	case update.CallbackQuery != nil:
		withFlow(ctx, cfg, bot, "callback", func(f onboard.Flow) error {
			return f.Callback(ctx, update.CallbackQuery)
		})
	case update.ChatJoinRequest != nil:
		withFlow(ctx, cfg, bot, "join request", func(f onboard.Flow) error {
			return f.JoinRequest(ctx, update.ChatJoinRequest)
		})
	case update.Message != nil:
		handleMessage(ctx, cfg, bot, update.Message)
	}
}

func handleMessage(ctx context.Context, cfg config.Config, bot telegram.Client, msg *telegram.Message) {
	cmd := msg.Command()
	private := msg.Chat.IsPrivate()

	// /start in private is the way into the group. Without both chat ids
	// there is nothing to join or nobody to approve, so it falls back to the
	// plain greeting below.
	if cmd == botcmd.CmdStart && private && onboardingEnabled(cfg) {
		withFlow(ctx, cfg, bot, "start", func(f onboard.Flow) error {
			if msg.From == nil {
				return nil
			}
			status, err := bot.GetMemberStatus(ctx, f.GroupID, msg.From.ID)
			if err != nil {
				return fmt.Errorf("member status: %w", err)
			}
			return f.Start(ctx, msg.From, status)
		})
		return
	}

	// /help (any chat) and /start (private only) are static text — no DB needed.
	if reply, ok := botcmd.StaticReply(cmd, private); ok {
		// /start is someone opening the bot in private for the first time, and
		// often the only sighting of a resident who reads the group chat but
		// never types a command in it. Worth the one connection this path
		// otherwise avoids; /help stays free of the database.
		if cmd == botcmd.CmdStart {
			recordSender(ctx, cfg, msg)
		}
		if err := bot.SendPlain(ctx, msg.Chat.ID, reply); err != nil {
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
	recordUser(ctx, conn, msg)

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

	if err := telegram.Send(ctx, http.DefaultClient, cfg.TelegramToken, msg.Chat.ID, text); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}

func onboardingEnabled(cfg config.Config) bool {
	_, group := cfg.GroupID()
	_, admin := cfg.AdminID()
	return group && admin
}

// withFlow runs one onboarding step with a DB connection of its own. Errors
// are logged under what, like every other webhook failure; the steps tell the
// user themselves when something went wrong on their side.
func withFlow(ctx context.Context, cfg config.Config, bot telegram.Client, what string, step func(onboard.Flow) error) {
	groupID, ok1 := cfg.GroupID()
	adminID, ok2 := cfg.AdminID()
	if !ok1 || !ok2 {
		return
	}
	conn, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Printf("webhook: %s: db connect: %v", what, err)
		return
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			log.Printf("webhook: %s: db close: %v", what, err)
		}
	}()
	f := onboard.Flow{Bot: bot, DB: conn, GroupID: groupID, AdminID: adminID, Now: time.Now}
	if err := step(f); err != nil {
		log.Printf("webhook: %s: %v", what, err)
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
