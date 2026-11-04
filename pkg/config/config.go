// Package config collects every environment variable the application reads
// into one place, so callers load it once instead of calling os.Getenv
// scattered across api/ and cmd/.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL   string
	TelegramToken string
	WebhookSecret string
	CronSecret    string
	// ChatID is the WG group (CHAT_ID); 0 when unset.
	ChatID int64
	// AdminChatID (a private chat or a group of admins) receives the
	// onboarding's approval cards and operational notices — a cron run that
	// could not do its job, the schedule running out — that only the
	// maintainers can act on. 0 (unset) disables them rather than falling
	// back to the group chat.
	AdminChatID int64
	// OnboardingEnabled is ONBOARDING_ENABLED read with strconv.ParseBool;
	// anything else, unset included, leaves the onboarding off.
	OnboardingEnabled bool
}

// Load reads the environment. A variable that is unset is left at its zero
// value: each caller decides what it can't do without. One that is set but
// can't be parsed is an error, naming every such variable. The Config holds
// everything that did parse either way, so a caller can still report the
// error through a channel that is configured correctly.
func Load() (Config, error) {
	onboarding, _ := strconv.ParseBool(os.Getenv("ONBOARDING_ENABLED"))
	chatID, chatErr := chatIDEnv("CHAT_ID")
	adminID, adminErr := chatIDEnv("ADMIN_CHAT_ID")
	return Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		CronSecret:    os.Getenv("CRON_SECRET"),
		ChatID:        chatID,
		AdminChatID:   adminID,

		OnboardingEnabled: onboarding,
	}, errors.Join(chatErr, adminErr)
}

// chatIDEnv reads a Telegram chat id: 0 when unset, an error unless it is a
// non-zero integer (no chat has id 0; a group's is negative).
func chatIDEnv(key string) (int64, error) {
	s := os.Getenv(key)
	if s == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("%s=%q is not a chat id", key, s)
	}
	return id, nil
}
