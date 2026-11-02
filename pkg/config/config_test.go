package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("WEBHOOK_SECRET", "wh-secret")
	t.Setenv("CRON_SECRET", "cron-secret")
	t.Setenv("CHAT_ID", "123")
	t.Setenv("ADMIN_CHAT_ID", "456")
	t.Setenv("ONBOARDING_ENABLED", "true")

	got := Load()
	want := Config{
		DatabaseURL:   "postgres://example",
		TelegramToken: "token",
		WebhookSecret: "wh-secret",
		CronSecret:    "cron-secret",
		ChatID:        "123",
		AdminChatID:   "456",

		OnboardingEnabled: true,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestOnboardingEnabled(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "1": true, "": false, "false": false, "yes": false} {
		t.Setenv("ONBOARDING_ENABLED", value)
		if got := Load().OnboardingEnabled; got != want {
			t.Errorf("ONBOARDING_ENABLED=%q: got %v, want %v", value, got, want)
		}
	}
}

func TestChatIDs(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"-1001234567890", -1001234567890, true},
		{"42", 42, true},
		{"", 0, false},
		{"0", 0, false},
		{"@wg", 0, false},
	}
	for _, tc := range cases {
		c := Config{ChatID: tc.in, AdminChatID: tc.in}
		if got, ok := c.GroupID(); got != tc.want || ok != tc.ok {
			t.Errorf("GroupID(%q) = %d, %v", tc.in, got, ok)
		}
		if got, ok := c.AdminID(); got != tc.want || ok != tc.ok {
			t.Errorf("AdminID(%q) = %d, %v", tc.in, got, ok)
		}
	}
}
