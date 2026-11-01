package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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

// handleMessage answers commands. In the WG group everyone gets them; other
// groups get nothing. In private the sender's role in the group decides:
// admins get every command, members a greeting, banned users nothing, and
// everybody else the onboarding.
func handleMessage(ctx context.Context, cfg config.Config, bot telegram.Client, msg *telegram.Message) {
	cmd := msg.Command()
	groupID, ok := cfg.GroupID()
	if cmd == "" || !ok || msg.Chat == nil || msg.From == nil {
		return
	}
	if msg.Chat.ID == groupID {
		answerCommand(ctx, cfg, bot, msg, cmd)
		return
	}
	if !msg.Chat.IsPrivate() {
		return
	}

	status, err := memberStatus(ctx, bot, groupID, msg.From.ID)
	if err != nil {
		log.Printf("webhook: member status: %v", err)
		return
	}

	switch {
	case status.IsAdmin() && cmd == botcmd.CmdStart:
		if err := bot.SetChatCommands(ctx, botcmd.Menu(), msg.Chat.ID); err != nil {
			log.Printf("webhook: set chat commands: %v", err)
		}
		if err := bot.SendPlain(ctx, msg.Chat.ID, botcmd.AdminStartReply()); err != nil {
			log.Printf("webhook: send admin start: %v", err)
		}
	case status.IsAdmin():
		answerCommand(ctx, cfg, bot, msg, cmd)
	case cmd != botcmd.CmdStart, status == telegram.StatusKicked:
		return
	case onboardingEnabled(cfg):
		withFlow(ctx, cfg, bot, "start", func(f onboard.Flow) error {
			return f.Start(ctx, msg.From, status)
		})
	default: // no ADMIN_CHAT_ID to approve requests: just greet
		reply, _ := botcmd.StaticReply(botcmd.CmdStart, true)
		if err := bot.SendPlain(ctx, msg.Chat.ID, reply); err != nil {
			log.Printf("webhook: send start: %v", err)
		}
	}
}

// memberStatus is the getChatMember lookup, a variable so tests need no Bot API.
var memberStatus = func(ctx context.Context, bot telegram.Client, chatID, userID int64) (telegram.MemberStatus, error) {
	return bot.GetMemberStatus(ctx, chatID, userID)
}

// answerCommand answers /help or a duty command; anything else is ignored.
func answerCommand(ctx context.Context, cfg config.Config, bot telegram.Client, msg *telegram.Message, cmd string) {
	if reply, ok := botcmd.StaticReply(cmd, msg.Chat.IsPrivate()); ok {
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
	if err := telegram.Send(ctx, bot.HTTP, bot.Token, msg.Chat.ID, text); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}

func onboardingEnabled(cfg config.Config) bool {
	_, _, ok := onboardingChats(cfg)
	return ok
}

// onboardingChats returns the WG group and the chat the approval cards go
// to, which may be the admin's private chat or a group of admins.
func onboardingChats(cfg config.Config) (groupID, adminID int64, ok bool) {
	groupID, okGroup := cfg.GroupID()
	adminID, okAdmin := cfg.AdminID()
	return groupID, adminID, okGroup && okAdmin
}

// withFlow runs one onboarding step on its own DB connection and logs its
// error under what.
func withFlow(ctx context.Context, cfg config.Config, bot telegram.Client, what string, step func(onboard.Flow) error) {
	groupID, adminID, ok := onboardingChats(cfg)
	if !ok {
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
