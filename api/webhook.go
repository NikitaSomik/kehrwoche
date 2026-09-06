package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	_ "time/tzdata"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitasomusev/kehrwoche/pkg/botcmd"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
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

	// A Telegram update is a few hundred bytes of JSON. Cap the read so an
	// oversized body can't burn this function's memory and invocation time.
	r.Body = http.MaxBytesReader(w, r.Body, maxUpdateBytes)

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

	// The webhook secret only proves the update came from Telegram, not that it
	// came from our flat. Check before anything else answers or touches the DB.
	if !allowedChat(ctx, cfg, update.Message) {
		return
	}

	cmd := update.Message.Command()

	// /help (any chat) and /start (private only) are static text — no DB needed.
	if reply, ok := botcmd.StaticReply(cmd, update.Message.Chat.IsPrivate()); ok {
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

// maxUpdateBytes caps the request body read from Telegram.
const maxUpdateBytes = 64 << 10

// memberCheck looks up a user's membership status in the flat's group chat.
// It's a variable so tests can substitute one instead of reaching the Bot API.
var memberCheck = func(ctx context.Context, cfg config.Config, groupID, userID int64) (string, error) {
	return telegram.GetChatMember(ctx, http.DefaultClient, cfg.TelegramToken, groupID, userID)
}

// allowedChat reports whether a message may be answered at all.
//
// The group chat must be ours. A private chat is allowed when its sender is a
// member of that group: the flat was told it can message the bot directly, so
// locking private chats out entirely would break a documented feature, while
// answering anyone who finds the bot by name hands a stranger the whole
// roster — who lives in which room and when they clean.
//
// Asking Telegram beats keeping a list of resident ids: nothing to collect,
// and membership follows the group automatically when someone moves in or out.
//
// Fails closed. An unset or unparseable CHAT_ID, a message with no sender, or
// a lookup that doesn't come back all deny.
func allowedChat(ctx context.Context, cfg config.Config, msg *tgbotapi.Message) bool {
	groupID, err := strconv.ParseInt(cfg.ChatID, 10, 64)
	if err != nil {
		log.Printf("webhook: invalid CHAT_ID: %v", err)
		return false
	}
	if msg.Chat == nil {
		return false
	}
	if msg.Chat.ID == groupID {
		return true
	}
	if !msg.Chat.IsPrivate() || msg.From == nil {
		return false
	}

	status, err := memberCheck(ctx, cfg, groupID, msg.From.ID)
	if err != nil {
		// Telegram errors rather than returning a status for a user it has
		// never seen in the chat, so this is the ordinary shape of "not one of
		// ours" as much as it is a genuine outage. Either way, no answer.
		log.Printf("webhook: chat member check: %v", err)
		return false
	}
	switch status {
	case telegram.StatusCreator, telegram.StatusAdministrator,
		telegram.StatusMember, telegram.StatusRestricted:
		return true
	default:
		// "left" and "kicked" — and anything Telegram adds later, since an
		// unknown status is not evidence of membership.
		return false
	}
}
