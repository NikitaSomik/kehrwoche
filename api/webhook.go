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

const maxUpdateBytes = 1 << 20

// Webhook receives Telegram updates: slash commands, button presses
// (callback_query), join requests (chat_join_request) and members leaving
// (chat_member). cmd/setwebhook's allowedUpdates must list every kind handled
// here.
//
// Auth is the X-Telegram-Bot-Api-Secret-Token header, compared in constant
// time and fail-closed. Once authenticated it always answers 200, because
// Telegram retries anything else and the retry duplicates messages; errors are
// only logged.
//
// Who gets what:
//   - In the WG group (CHAT_ID) everyone gets the commands: posting there
//     already proves membership. Other groups get nothing.
//   - In private, one getChatMember lookup of the sender in the group decides
//     (fail-closed on error or unset CHAT_ID). Group admins get every command,
//     and their /start sets the command menu for their chat and lists them.
//   - For anybody else only /start counts: members get a greeting, banned users
//     nothing, the rest the onboarding (pkg/onboard). While the onboarding is
//     on, anything else they write gets botcmd.JoinHint; while it is off
//     (ONBOARDING_ENABLED not true, or CHAT_ID/ADMIN_CHAT_ID unset, see
//     onboardingChats) /start is a plain greeting.
//
// Button presses, join requests and member updates go to onboard.Flow, gated
// by the same onboardingChats. recordUser records whoever runs a duty command.
func Webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cfg, cfgErr := config.Load()

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

	// A variable that doesn't parse is left unset, which turns off what
	// depends on it (an unreadable ADMIN_CHAT_ID turns off the onboarding);
	// the rest keeps working. Logged on every update until it is fixed.
	if cfgErr != nil {
		log.Printf("webhook: config: %v", cfgErr)
	}

	// Updates are a few KB; the cap stops a caller holding the secret from
	// making the function read without bound.
	var update telegram.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUpdateBytes)).Decode(&update); err != nil {
		log.Printf("webhook: decode: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	bot := telegram.New(cfg.TelegramToken)
	switch {
	case update.CallbackQuery != nil:
		withFlow(ctx, cfg, bot, "callback", func(f onboard.Flow) error {
			return f.Callback(ctx, update.CallbackQuery)
		})
	case update.ChatJoinRequest != nil:
		withFlow(ctx, cfg, bot, "join request", func(f onboard.Flow) error {
			return f.JoinRequest(ctx, update.ChatJoinRequest)
		})
	case update.ChatMember != nil:
		withFlow(ctx, cfg, bot, "member update", func(f onboard.Flow) error {
			return f.MemberUpdate(ctx, update.ChatMember)
		})
	case update.Message != nil:
		handleMessage(ctx, cfg, bot, update.Message)
	}
}

// handleMessage answers commands. In the WG group everyone gets them; other
// groups get nothing. In private the sender's role in the group decides:
// admins get every command, members a greeting, banned users nothing, and
// everybody else the onboarding, or a pointer to /start for anything else
// they write.
func handleMessage(ctx context.Context, cfg config.Config, bot *telegram.Client, msg *telegram.Message) {
	cmd := msg.Command()
	groupID := cfg.ChatID
	if groupID == 0 || msg.Chat == nil || msg.From == nil {
		return
	}
	if msg.Chat.ID == groupID {
		if cmd != "" {
			answerCommand(ctx, cfg, bot, msg, cmd)
		}
		return
	}
	if !msg.Chat.IsPrivate() {
		return
	}

	status, err := bot.GetMemberStatus(ctx, groupID, msg.From.ID)
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
		if cmd != "" {
			answerCommand(ctx, cfg, bot, msg, cmd)
		}
	case status == telegram.StatusKicked:
		return
	case cmd != botcmd.CmdStart:
		if !status.InChat() && onboardingEnabled(cfg) {
			if err := bot.SendPlain(ctx, msg.Chat.ID, botcmd.JoinHint); err != nil {
				log.Printf("webhook: send join hint: %v", err)
			}
		}
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

func answerCommand(ctx context.Context, cfg config.Config, bot *telegram.Client, msg *telegram.Message, cmd string) {
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
	if err := bot.Send(ctx, msg.Chat.ID, text); err != nil {
		log.Printf("webhook: send: %v", err)
	}
}

func onboardingEnabled(cfg config.Config) bool {
	_, _, ok := onboardingChats(cfg)
	return ok
}

func onboardingChats(cfg config.Config) (groupID, adminID int64, ok bool) {
	return cfg.ChatID, cfg.AdminChatID, cfg.OnboardingEnabled && cfg.ChatID != 0 && cfg.AdminChatID != 0
}

func withFlow(ctx context.Context, cfg config.Config, bot *telegram.Client, what string, step func(onboard.Flow) error) {
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

// recordUser only logs a failure: knowing who uses the bot must never cost
// somebody an answer.
func recordUser(ctx context.Context, conn users.Execer, msg *telegram.Message) {
	if msg.From == nil {
		return
	}
	if err := users.Record(ctx, conn, msg.From.ID); err != nil {
		log.Printf("webhook: record user: %v", err)
	}
}
