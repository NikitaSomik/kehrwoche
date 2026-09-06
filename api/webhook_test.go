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
	"github.com/jackc/pgx/v5/pgconn"
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

type recordingExecer struct {
	calls int
	args  []any
	err   error
}

func (r *recordingExecer) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.calls++
	r.args = args
	return pgconn.CommandTag{}, r.err
}

func TestRecordUser(t *testing.T) {
	t.Run("stores the sender", func(t *testing.T) {
		e := &recordingExecer{}
		msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 987654321}}

		recordUser(context.Background(), e, msg)

		if e.calls != 1 {
			t.Fatalf("got %d writes, want 1", e.calls)
		}
		if len(e.args) != 1 || e.args[0] != int64(987654321) {
			t.Errorf("got args %v, want the sender's id", e.args)
		}
	})

	t.Run("a message with no sender writes nothing", func(t *testing.T) {
		e := &recordingExecer{}

		recordUser(context.Background(), e, &tgbotapi.Message{})

		if e.calls != 0 {
			t.Errorf("got %d writes, want none", e.calls)
		}
	})

	// The reply matters more than the bookkeeping, so a failed write is
	// swallowed rather than surfaced.
	t.Run("a failed write does not panic", func(t *testing.T) {
		e := &recordingExecer{err: errors.New("connection reset")}
		msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}}

		recordUser(context.Background(), e, msg)
	})
}
