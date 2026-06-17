package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

type Bot struct {
	api     *tgbotapi.BotAPI
	cfg     *config.Config
	entries []schedule.Entry
}

func New(cfg *config.Config, entries []schedule.Entry) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, err
	}
	log.Printf("authorized as @%s", api.Self.UserName)
	return &Bot{api: api, cfg: cfg, entries: entries}, nil
}

// loadEntries reloads schedule from disk, falling back to in-memory entries on error.
func (b *Bot) loadEntries() []schedule.Entry {
	entries, err := schedule.Load(b.cfg.SchedulePath)
	if err != nil {
		return b.entries
	}
	b.entries = entries
	return entries
}

func (b *Bot) SendWeeklyReminder() {
	entries := b.loadEntries()
	now := time.Now().In(b.cfg.Location)
	room, ok := schedule.OnDuty(entries, now)
	window := schedule.CleaningWindow(now)

	var text string
	if !ok {
		text = fmt.Sprintf("🚽 *Toilette — %s*\n\nKeine Planung für diese Woche.", window)
	} else {
		text = fmt.Sprintf("🚽 *Toilette — %s*\n\n%s ist dran!", window, room)
	}

	b.send(b.cfg.ChatID, text)
}

func (b *Bot) Poll(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(update.Message)
		}
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	entries := b.loadEntries()
	switch msg.Command() {
	case "wer":
		now := time.Now().In(b.cfg.Location)
		room, ok := schedule.OnDuty(entries, now)
		window := schedule.CleaningWindow(now)
		var text string
		if !ok {
			text = fmt.Sprintf("❓ %s: keine Planung.", window)
		} else {
			text = fmt.Sprintf("🚽 %s: *%s*", window, room)
		}
		b.send(msg.Chat.ID, text)

	case "plan":
		upcoming := schedule.Upcoming(entries, time.Now().In(b.cfg.Location), 4)
		lines := make([]string, len(upcoming))
		for i, e := range upcoming {
			t := schedule.ParseWeekKey(e.Week)
			window := schedule.CleaningWindow(t)
			room := e.Room
			if room == "" {
				room = "—"
			}
			lines[i] = fmt.Sprintf("%s: %s", window, room)
		}
		text := "📅 *Plan — nächste 4 Wochen:*\n\n" + strings.Join(lines, "\n")
		b.send(msg.Chat.ID, text)
	}
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send to %d: %v", chatID, err)
	}
}
