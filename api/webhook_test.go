package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

// commandUpdateJSON builds a minimal Telegram update JSON body containing a
// single bot_command message, which is the shape Message.Command() requires
// (a bot_command entity at offset 0).
func commandUpdateJSON(command string) string {
	text := "/" + command
	return `{"update_id":1,"message":{"message_id":1,"date":1,` +
		`"chat":{"id":123,"type":"group"},"text":"` + text + `",` +
		`"entities":[{"type":"bot_command","offset":0,"length":` +
		strconv.Itoa(len(text)) + `}]}}`
}

func TestWebhook_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhook", nil)
	rec := httptest.NewRecorder()

	Webhook(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhook_Unauthorized(t *testing.T) {
	cases := []struct {
		name      string
		secretEnv string
		header    string
	}{
		{"secret not configured, no header", "", ""},
		{"secret not configured, header sent anyway", "", "s3cret"},
		{"secret configured, no header", "s3cret", ""},
		{"secret configured, wrong header", "s3cret", "wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_SECRET", tc.secretEnv)
			req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(""))
			if tc.header != "" {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.header)
			}
			rec := httptest.NewRecorder()

			Webhook(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// TestWebhook_AuthorizedRequests covers request bodies that must not panic and
// must always answer 200 once authorized — Telegram retries any non-200
// response, so a transient error must never surface as an HTTP error here.
func TestWebhook_AuthorizedRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed JSON", "not json"},
		{"no message field", `{"update_id":1}`},
		{"unknown command", commandUpdateJSON("nope")},
		{"/start in a group falls through and is ignored", commandUpdateJSON("start")},
		{"known command, no DB available", commandUpdateJSON("toilette1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_SECRET", "s3cret")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("CHAT_ID", "123") // the group commandUpdateJSON builds
			req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(tc.body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
			rec := httptest.NewRecorder()

			Webhook(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}

// stubMemberCheck swaps the Bot API lookup for the duration of a test.
func stubMemberCheck(t *testing.T, status string, err error) {
	t.Helper()
	prev := memberCheck
	memberCheck = func(ctx context.Context, cfg config.Config, groupID, userID int64) (string, error) {
		return status, err
	}
	t.Cleanup(func() { memberCheck = prev })
}

func groupMsg(chatID int64) *tgbotapi.Message {
	return &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID, Type: "group"}}
}

func privateMsg(chatID, fromID int64) *tgbotapi.Message {
	return &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: chatID, Type: "private"},
		From: &tgbotapi.User{ID: fromID},
	}
}

func TestAllowedChat(t *testing.T) {
	const group = int64(123)
	cfg := config.Config{ChatID: "123"}

	t.Run("our group is allowed without asking Telegram", func(t *testing.T) {
		stubMemberCheck(t, "", errors.New("must not be called for a group chat"))
		if !allowedChat(context.Background(), cfg, groupMsg(group)) {
			t.Error("the flat's own group must be allowed")
		}
	})

	t.Run("another group is refused", func(t *testing.T) {
		stubMemberCheck(t, "", errors.New("must not be called for a group chat"))
		if allowedChat(context.Background(), cfg, groupMsg(999)) {
			t.Error("a group that is not ours must be refused")
		}
	})

	t.Run("private chats follow group membership", func(t *testing.T) {
		cases := []struct {
			status string
			want   bool
		}{
			{telegram.StatusCreator, true},
			{telegram.StatusAdministrator, true},
			{telegram.StatusMember, true},
			{telegram.StatusRestricted, true},
			{"left", false},
			{"kicked", false},
			{"something_telegram_added_later", false},
		}
		for _, tc := range cases {
			t.Run(tc.status, func(t *testing.T) {
				stubMemberCheck(t, tc.status, nil)
				if got := allowedChat(context.Background(), cfg, privateMsg(7, 7)); got != tc.want {
					t.Errorf("status %q: got %v, want %v", tc.status, got, tc.want)
				}
			})
		}
	})

	t.Run("a failed lookup denies", func(t *testing.T) {
		stubMemberCheck(t, "", errors.New("Bad Request: user not found"))
		if allowedChat(context.Background(), cfg, privateMsg(7, 7)) {
			t.Error("an unanswered membership lookup must fail closed")
		}
	})

	t.Run("no CHAT_ID denies everyone", func(t *testing.T) {
		stubMemberCheck(t, telegram.StatusMember, nil)
		if allowedChat(context.Background(), config.Config{}, groupMsg(group)) {
			t.Error("an unconfigured CHAT_ID must fail closed")
		}
		if allowedChat(context.Background(), config.Config{}, privateMsg(7, 7)) {
			t.Error("an unconfigured CHAT_ID must fail closed")
		}
	})

	t.Run("a message with no sender denies", func(t *testing.T) {
		stubMemberCheck(t, telegram.StatusMember, nil)
		msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 7, Type: "private"}}
		if allowedChat(context.Background(), cfg, msg) {
			t.Error("a private message with no From must be refused")
		}
	})
}

// A stranger who found the bot by name must get no answer and cost no DB
// connection — the whole point of the check.
func TestWebhook_StrangerIsIgnored(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "s3cret")
	t.Setenv("CHAT_ID", "123")
	t.Setenv("DATABASE_URL", "postgres://must.not.be.dialled/db")
	stubMemberCheck(t, "left", nil)

	body := `{"update_id":1,"message":{"message_id":1,"date":1,` +
		`"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/etage",` +
		`"entities":[{"type":"bot_command","offset":0,"length":6}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
	rec := httptest.NewRecorder()

	Webhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

// An oversized body is cut off by MaxBytesReader; decoding then fails and the
// handler still answers 200 rather than letting Telegram retry.
func TestWebhook_OversizedBody(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "s3cret")
	t.Setenv("CHAT_ID", "123")
	body := `{"update_id":1,"message":{"text":"` + strings.Repeat("a", maxUpdateBytes+1) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
	rec := httptest.NewRecorder()

	Webhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}
