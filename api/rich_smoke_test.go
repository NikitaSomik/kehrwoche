//go:build smoke

package handler

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/botcmd"
	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

// TestRichMessagesReachAdmin really talks to Telegram: run it with
// `task smoke:rich`. It sends what the group would get, rich messages and the
// new buttons, to ADMIN_CHAT_ID only, and fails if Telegram refused any of it.
// SendRich would hide a refusal behind its plain fallback, so the transport
// records every Bot API method called: a sendMessage means a fallback.
func TestRichMessagesReachAdmin(t *testing.T) {
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "ADMIN_CHAT_ID"} {
		if os.Getenv(key) == "" {
			t.Skipf("%s is not set; run through `task smoke:rich` so .env is loaded", key)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	hc := &http.Client{
		Timeout: telegram.DefaultTimeout,
		Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			return http.DefaultTransport.RoundTrip(r)
		}),
	}
	bot := telegram.New(cfg.TelegramToken, telegram.WithHTTPClient(hc))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin := cfg.AdminChatID

	help, _ := botcmd.StaticReply(botcmd.CmdHelp, true)
	messages := map[string]telegram.Rich{
		"/help": help,
		"reminder": reminder("Fr 14.11 – So 16.11", [][]string{
			{"Etage", "Zimmer 3"}, {"Toilette 1", "Zimmer 5"}, {"Toilette 2", "—"}, {"Treppenhaus", "Zimmer 1"},
		}),
	}
	for name, m := range messages {
		methods = nil
		if err := bot.SendRich(ctx, admin, m); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Join(methods, ",") != "sendRichMessage" {
			t.Errorf("%s: Telegram refused the rich message, the plain copy went out instead (calls %v)", name, methods)
		}
	}

	rows := [][]telegram.Button{
		{
			{Text: "✅ Annehmen", Data: "smoke:1", Style: telegram.StyleSuccess},
			{Text: "❌ Ablehnen", Data: "smoke:2", Style: telegram.StyleDanger},
		},
		{{Text: "✅ Angenommen von Smoke", Disabled: &telegram.DisabledButton{}}},
	}
	if err := bot.SendKeyboard(ctx, admin, "Smoke-Test: grüne und rote Taste, darunter eine deaktivierte.", rows); err != nil {
		t.Errorf("buttons: %v", err)
	}
	t.Log("check your private chat: /help, the reminder table and the buttons")
}
