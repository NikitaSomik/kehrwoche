// Command setwebhook re-registers the bot's webhook with allowedUpdates and
// WEBHOOK_SECRET, keeping the URL Telegram already has. It hits the Telegram
// API — run it by hand.
//
//	task setwebhook            # re-register with allowedUpdates
//	task setwebhook -- -show    # print the current registration, change nothing
//	task setwebhook -- -url https://example.vercel.app/api/webhook   # first time, or a new URL
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

// allowedUpdates must list every update kind api/webhook.go handles.
var allowedUpdates = []string{"message", "callback_query", "chat_join_request", "chat_member"}

func main() {
	show := flag.Bool("show", false, "print the current webhook registration and exit")
	url := flag.String("url", "", "webhook URL; default: keep the one Telegram has")
	flag.Parse()

	if err := run(context.Background(), *show, *url); err != nil {
		fmt.Fprintln(os.Stderr, "setwebhook:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, show bool, url string) error {
	cfg := config.Load()
	if cfg.TelegramToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}

	info, err := telegram.GetWebhookInfo(ctx, http.DefaultClient, cfg.TelegramToken)
	if err != nil {
		return err
	}
	if show {
		printInfo(info)
		return nil
	}

	if url == "" {
		url = info.URL
	}
	if url == "" {
		return fmt.Errorf("no webhook registered yet; pass -url")
	}
	if cfg.WebhookSecret == "" {
		return fmt.Errorf("WEBHOOK_SECRET is not set")
	}
	if err := telegram.SetWebhook(ctx, http.DefaultClient, cfg.TelegramToken, url, cfg.WebhookSecret, allowedUpdates); err != nil {
		return err
	}
	fmt.Printf("webhook set: %s\nallowed updates: %s\n", url, strings.Join(allowedUpdates, ", "))
	return nil
}

func printInfo(info telegram.WebhookInfo) {
	url := info.URL
	if url == "" {
		url = "(none)"
	}
	allowed := "(all except chat_member, message_reaction, message_reaction_count)"
	if len(info.AllowedUpdates) > 0 {
		allowed = strings.Join(info.AllowedUpdates, ", ")
	}
	fmt.Printf("url: %s\nallowed updates: %s\npending updates: %d\n", url, allowed, info.PendingUpdateCount)
	if info.LastErrorMessage != "" {
		fmt.Printf("last error: %s\n", info.LastErrorMessage)
	}
}
