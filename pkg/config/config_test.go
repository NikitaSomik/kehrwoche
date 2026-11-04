package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("WEBHOOK_SECRET", "wh-secret")
	t.Setenv("CRON_SECRET", "cron-secret")
	t.Setenv("CHAT_ID", "-1001234567890")
	t.Setenv("ADMIN_CHAT_ID", "456")
	t.Setenv("ONBOARDING_ENABLED", "true")

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		DatabaseURL:   "postgres://example",
		TelegramToken: "token",
		WebhookSecret: "wh-secret",
		CronSecret:    "cron-secret",
		ChatID:        -1001234567890,
		AdminChatID:   456,

		OnboardingEnabled: true,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestOnboardingEnabled(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "1": true, "": false, "false": false, "yes": false} {
		t.Setenv("ONBOARDING_ENABLED", value)
		got, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if got.OnboardingEnabled != want {
			t.Errorf("ONBOARDING_ENABLED=%q: got %v, want %v", value, got.OnboardingEnabled, want)
		}
	}
}

func TestChatIDs(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		invalid bool
	}{
		{"-1001234567890", -1001234567890, false},
		{"42", 42, false},
		{"", 0, false}, // unset: the caller decides
		{"0", 0, true},
		{"@wg", 0, true},
		{" 42", 0, true},
	}
	for _, tc := range cases {
		t.Setenv("CHAT_ID", tc.in)
		t.Setenv("ADMIN_CHAT_ID", tc.in)

		cfg, err := Load()
		if cfg.ChatID != tc.want || cfg.AdminChatID != tc.want {
			t.Errorf("%q: got %d and %d, want %d", tc.in, cfg.ChatID, cfg.AdminChatID, tc.want)
		}
		if (err != nil) != tc.invalid {
			t.Errorf("%q: error %v, want invalid=%v", tc.in, err, tc.invalid)
		}
		if !tc.invalid {
			continue
		}
		// errors.Join puts one per line.
		if msg := err.Error(); !strings.HasPrefix(msg, "CHAT_ID=") || !strings.Contains(msg, "\nADMIN_CHAT_ID=") {
			t.Errorf("%q: error %q must name both variables", tc.in, msg)
		}
	}
}

// A bad ADMIN_CHAT_ID must not cost the rest: the cron still reminds the
// group, and the caller still gets every value that did parse.
func TestLoadKeepsWhatParsed(t *testing.T) {
	t.Setenv("CHAT_ID", "-100")
	t.Setenv("ADMIN_CHAT_ID", "admins")
	t.Setenv("CRON_SECRET", "s")

	cfg, err := Load()
	if err == nil || !strings.HasPrefix(err.Error(), "ADMIN_CHAT_ID=") || strings.Contains(err.Error(), "\n") {
		t.Errorf("error %v, want only ADMIN_CHAT_ID named", err)
	}
	if cfg.ChatID != -100 || cfg.CronSecret != "s" || cfg.AdminChatID != 0 {
		t.Errorf("got %+v", cfg)
	}
}
