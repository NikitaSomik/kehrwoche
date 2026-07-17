// Package config collects every environment variable the application reads
// into one place, so callers load it once instead of calling os.Getenv
// scattered across api/ and cmd/.
package config

import "os"

type Config struct {
	DatabaseURL   string
	TelegramToken string
	WebhookSecret string
	CronSecret    string
	ChatID        string
}

func Load() Config {
	return Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		CronSecret:    os.Getenv("CRON_SECRET"),
		ChatID:        os.Getenv("CHAT_ID"),
	}
}
