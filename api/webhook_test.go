package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
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
		{"callback with onboarding off", `{"update_id":1,"callback_query":{"id":"x","from":{"id":1},"data":"room:3"}}`},
		{"body over the cap", `{"update_id":1,"pad":"` + strings.Repeat("a", maxUpdateBytes) + `"}`},
		{"member update with onboarding off", `{"update_id":1,"chat_member":{"chat":{"id":-100},"from":{"id":1},"old_chat_member":{"status":"member","user":{"id":1}},"new_chat_member":{"status":"left","user":{"id":1}}}}`},
		{"join request with onboarding off", `{"update_id":1,"chat_join_request":{"chat":{"id":-100},"from":{"id":1},"user_chat_id":1}}`},
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
		msg := &telegram.Message{From: &telegram.User{ID: 987654321}}

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

		recordUser(context.Background(), e, &telegram.Message{})

		if e.calls != 0 {
			t.Errorf("got %d writes, want none", e.calls)
		}
	})

	// The reply matters more than the bookkeeping, so a failed write is
	// swallowed rather than surfaced.
	t.Run("a failed write does not panic", func(t *testing.T) {
		e := &recordingExecer{err: errors.New("connection reset")}
		msg := &telegram.Message{From: &telegram.User{ID: 1}}

		recordUser(context.Background(), e, msg)
	})
}

type apiCall struct{ method, body string }

// recordingBot is a telegram.Client whose requests are recorded, not sent.
func recordingBot(calls *[]apiCall) *telegram.Client {
	return telegram.New("T", telegram.WithHTTPClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, apiCall{method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], body: string(b)})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}))
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHandleMessageRouting(t *testing.T) {
	orig := memberStatus
	t.Cleanup(func() { memberStatus = orig })

	private := func(cmd string) *telegram.Message {
		return &telegram.Message{
			From: &telegram.User{ID: 7}, Chat: &telegram.Chat{ID: 7, Type: "private"}, Text: "/" + cmd,
			Entities: []telegram.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(cmd) + 1}},
		}
	}
	inGroup := func(cmd string) *telegram.Message {
		m := private(cmd)
		m.Chat = &telegram.Chat{ID: -100, Type: "supergroup"}
		return m
	}
	inOtherGroup := func(cmd string) *telegram.Message {
		m := private(cmd)
		m.Chat = &telegram.Chat{ID: -200, Type: "supergroup"}
		return m
	}
	// ADMIN_CHAT_ID is left unset so /start from non-admins takes the plain
	// greeting rather than the onboarding, which needs a database.
	cfg := config.Config{ChatID: "-100"}

	cases := []struct {
		name    string
		msg     *telegram.Message
		status  telegram.MemberStatus
		methods []string
		contain string
	}{
		{"admin /start gets the menu and the command list", private("start"), telegram.StatusCreator, []string{"setMyCommands", "sendMessage"}, "/etage_plan"},
		{"admin /help", private("help"), telegram.StatusAdministrator, []string{"sendMessage"}, "/etage"},
		{"member /start is greeted", private("start"), telegram.StatusMember, []string{"sendMessage"}, "Hallo"},
		{"member commands are ignored", private("help"), telegram.StatusMember, nil, ""},
		{"stranger commands are ignored", private("etage"), telegram.StatusLeft, nil, ""},
		{"banned /start is ignored", private("start"), telegram.StatusKicked, nil, ""},
		{"anyone in the WG group gets /help", inGroup("help"), telegram.StatusMember, []string{"sendMessage"}, "/etage"},
		{"/start in the WG group is ignored", inGroup("start"), telegram.StatusMember, nil, ""},
		{"another group gets nothing", inOtherGroup("help"), telegram.StatusCreator, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memberStatus = func(context.Context, *telegram.Client, int64, int64) (telegram.MemberStatus, error) {
				return tc.status, nil
			}
			var calls []apiCall
			handleMessage(context.Background(), cfg, recordingBot(&calls), tc.msg)

			var methods []string
			for _, c := range calls {
				methods = append(methods, c.method)
			}
			if strings.Join(methods, ",") != strings.Join(tc.methods, ",") {
				t.Fatalf("calls %v, want %v", methods, tc.methods)
			}
			if tc.contain != "" && !strings.Contains(calls[len(calls)-1].body, tc.contain) {
				t.Errorf("reply %s lacks %q", calls[len(calls)-1].body, tc.contain)
			}
		})
	}

	t.Run("a failed lookup answers nothing", func(t *testing.T) {
		memberStatus = func(context.Context, *telegram.Client, int64, int64) (telegram.MemberStatus, error) {
			return "", errors.New("timeout")
		}
		var calls []apiCall
		handleMessage(context.Background(), cfg, recordingBot(&calls), private("start"))
		if len(calls) != 0 {
			t.Errorf("calls %v", calls)
		}
	})

	t.Run("plain text outside the group points to /start while onboarding is on", func(t *testing.T) {
		on := config.Config{ChatID: "-100", AdminChatID: "42", OnboardingEnabled: true}
		text := func(chat *telegram.Chat) *telegram.Message {
			return &telegram.Message{From: &telegram.User{ID: 7}, Chat: chat, Text: "hallo"}
		}
		for name, c := range map[string]struct {
			cfg    config.Config
			chat   *telegram.Chat
			status telegram.MemberStatus
			hint   bool
		}{
			"stranger":           {on, &telegram.Chat{ID: 7, Type: "private"}, telegram.StatusLeft, true},
			"stranger, flag off": {cfg, &telegram.Chat{ID: 7, Type: "private"}, telegram.StatusLeft, false},
			"member":             {on, &telegram.Chat{ID: 7, Type: "private"}, telegram.StatusMember, false},
			"admin":              {on, &telegram.Chat{ID: 7, Type: "private"}, telegram.StatusCreator, false},
			"banned":             {on, &telegram.Chat{ID: 7, Type: "private"}, telegram.StatusKicked, false},
			"in the WG group":    {on, &telegram.Chat{ID: -100, Type: "supergroup"}, telegram.StatusLeft, false},
		} {
			memberStatus = func(context.Context, *telegram.Client, int64, int64) (telegram.MemberStatus, error) {
				return c.status, nil
			}
			var calls []apiCall
			handleMessage(context.Background(), c.cfg, recordingBot(&calls), text(c.chat))
			got := len(calls) == 1 && strings.Contains(calls[0].body, "/start")
			if got != c.hint || (!c.hint && len(calls) != 0) {
				t.Errorf("%s: calls %v, want hint=%v", name, calls, c.hint)
			}
		}
	})

	t.Run("CHAT_ID unset answers nothing", func(t *testing.T) {
		var calls []apiCall
		handleMessage(context.Background(), config.Config{}, recordingBot(&calls), private("start"))
		if len(calls) != 0 {
			t.Errorf("calls %v", calls)
		}
	})
}

func TestOnboardingChats(t *testing.T) {
	cases := []struct {
		name, group, admin string
		enabled            bool
		want               bool
	}{
		{"enabled, both set", "-1001", "42", true, true},
		{"disabled", "-1001", "42", false, false},
		{"admin unset", "-1001", "", true, false},
		{"group unset", "", "42", true, false},
		{"admin chat is a group", "-1001", "-1002", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ok := onboardingChats(config.Config{ChatID: c.group, AdminChatID: c.admin, OnboardingEnabled: c.enabled})
			if ok != c.want {
				t.Errorf("ok = %v, want %v", ok, c.want)
			}
		})
	}
}
