package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultScheduleWeeks = 8

type Config struct {
	Token         string
	Rooms         []string
	ChatID        int64
	SchedulePath  string
	ScheduleWeeks int
	Location      *time.Location
}

func LoadFromEnv() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}

	roomsRaw := os.Getenv("ROOMS")
	if roomsRaw == "" {
		return nil, fmt.Errorf("ROOMS is not set")
	}
	rooms := strings.Split(roomsRaw, ",")
	for i, r := range rooms {
		rooms[i] = strings.TrimSpace(r)
	}
	for _, r := range rooms {
		if r == "" {
			return nil, fmt.Errorf("ROOMS contains an empty entry")
		}
	}

	chatID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("CHAT_ID")), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("CHAT_ID is invalid or not set")
	}

	schedulePath := os.Getenv("SCHEDULE_PATH")
	if schedulePath == "" {
		schedulePath = "schedule.json"
	}

	weeks := defaultScheduleWeeks
	if v, err := strconv.Atoi(os.Getenv("SCHEDULE_WEEKS")); err == nil && v > 0 {
		weeks = v
	}

	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "Europe/Berlin"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid TZ %q: %w", tz, err)
	}

	return &Config{
		Token:         token,
		Rooms:         rooms,
		ChatID:        chatID,
		SchedulePath:  schedulePath,
		ScheduleWeeks: weeks,
		Location:      loc,
	}, nil
}
