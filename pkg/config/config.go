// Package config collects every environment variable the application reads
// into one place, so callers load it once instead of calling os.Getenv
// scattered across api/ and cmd/.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL   string
	TelegramToken string
	WebhookSecret string
	CronSecret    string
	ChatID        string
	// AdminChatID (a private chat or a group of admins) receives the
	// onboarding's approval cards and operational notices — a cron run that
	// could not do its job, the schedule running out — that only the
	// maintainers can act on. Empty disables them rather than falling back to
	// the group chat.
	AdminChatID string
	// OnboardingEnabled is ONBOARDING_ENABLED read with strconv.ParseBool;
	// anything else, unset included, leaves the onboarding off.
	OnboardingEnabled bool
}

func Load() Config {
	onboarding, _ := strconv.ParseBool(os.Getenv("ONBOARDING_ENABLED"))
	return Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		CronSecret:    os.Getenv("CRON_SECRET"),
		ChatID:        os.Getenv("CHAT_ID"),
		AdminChatID:   os.Getenv("ADMIN_CHAT_ID"),

		OnboardingEnabled: onboarding,
	}
}

// GroupID is CHAT_ID as a number; ok is false when unset or invalid.
func (c Config) GroupID() (id int64, ok bool) {
	return parseChatID(c.ChatID)
}

// AdminID is ADMIN_CHAT_ID as a number; ok is false when unset or invalid.
func (c Config) AdminID() (id int64, ok bool) {
	return parseChatID(c.AdminChatID)
}

func parseChatID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}
