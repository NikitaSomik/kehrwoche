package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("WEBHOOK_SECRET", "wh-secret")
	t.Setenv("CRON_SECRET", "cron-secret")
	t.Setenv("CHAT_ID", "123")
	t.Setenv("ADMIN_CHAT_ID", "456")

	got := Load()
	want := Config{
		DatabaseURL:   "postgres://example",
		TelegramToken: "token",
		WebhookSecret: "wh-secret",
		CronSecret:    "cron-secret",
		ChatID:        "123",
		AdminChatID:   "456",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
