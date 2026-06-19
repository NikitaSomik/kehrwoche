package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nikitasomusev/kehrwoche/pkg/bot"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func main() {
	godotenv.Load() //nolint:errcheck,gosec // .env is optional, missing file is expected in production

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	log.Printf("timezone: %s", cfg.Location)

	entries, err := loadOrGenerate(cfg)
	if err != nil {
		log.Fatalf("schedule: %v", err)
	}

	b, err := bot.New(cfg, entries)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	if os.Getenv("SEND_NOW") == "1" {
		b.SendWeeklyReminder()
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scheduleThursday(ctx, b, cfg.Location)
	b.Poll(ctx)
}

func loadOrGenerate(cfg *config.Config) ([]schedule.Entry, error) {
	existing, err := schedule.Load(cfg.SchedulePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("corrupt schedule file %s: %w", cfg.SchedulePath, err)
		}
		log.Printf("no schedule found, generating %d weeks...", cfg.ScheduleWeeks)
		existing = nil
	} else {
		log.Printf("loaded %d schedule entries from %s", len(existing), cfg.SchedulePath)
	}

	entries := schedule.Extend(existing, cfg.Rooms, cfg.ScheduleWeeks)
	if err := schedule.Save(cfg.SchedulePath, entries); err != nil {
		log.Printf("warning: could not save schedule: %v", err)
	}
	return entries, nil
}

func scheduleThursday(ctx context.Context, b *bot.Bot, loc *time.Location) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scheduleThursday panic: %v", r)
		}
	}()
	for {
		next := nextThursday(time.Now().In(loc))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			b.SendWeeklyReminder()
		}
	}
}

func nextThursday(from time.Time) time.Time {
	d := (time.Thursday - from.Weekday() + 7) % 7
	if d == 0 {
		d = 7
	}
	t := from.AddDate(0, 0, int(d))
	return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, from.Location())
}
