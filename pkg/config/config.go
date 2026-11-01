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
	// AdminChatID receives operational notices — a cron run that could not do
	// its job, the schedule running out — that only the maintainer can act on.
	// Empty disables them
	// rather than falling back to the group chat.
	AdminChatID string
}

func Load() Config {
	return Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		CronSecret:    os.Getenv("CRON_SECRET"),
		ChatID:        os.Getenv("CHAT_ID"),
		AdminChatID:   os.Getenv("ADMIN_CHAT_ID"),
	}
}

// GroupID is CHAT_ID as a number: the WG group the bot serves. ok is false
// when it is unset or not a number.
func (c Config) GroupID() (id int64, ok bool) {
	return parseChatID(c.ChatID)
}

// AdminID is ADMIN_CHAT_ID as a number. ok is false when it is unset or not a
// number.
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
