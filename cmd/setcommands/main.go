// Command setcommands pushes the bot's command menu to Telegram via
// setMyCommands, built from the same list the webhook serves (botcmd.Menu).
// It changes the bot's public config and hits the Telegram API — run it by hand.
//
//	task setcommands           # push the command menu
//	task setcommands -- -show   # print Telegram's current menu, change nothing
//
// The list is pushed to every scope BotFather exposes (default + private chats
// + group chats + group admins), so a scope-specific list left over from an
// earlier BotFather edit can't shadow it.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/nikitasomusev/kehrwoche/pkg/botcmd"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

// menuScopes: "" is the default scope; the rest are the BotCommandScope types
// BotFather shows as toggles.
var menuScopes = []string{"", "all_private_chats", "all_group_chats", "all_chat_administrators"}

func scopeName(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

func main() {
	show := flag.Bool("show", false, "print Telegram's current command menu and exit")
	flag.Parse()

	if err := run(context.Background(), *show); err != nil {
		fmt.Fprintln(os.Stderr, "setcommands:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, show bool) error {
	token := config.Load().TelegramToken
	if token == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}

	if show {
		for _, s := range menuScopes {
			cmds, err := telegram.GetCommands(ctx, http.DefaultClient, token, s)
			if err != nil {
				return fmt.Errorf("scope %s: %w", scopeName(s), err)
			}
			fmt.Printf("[%s]\n", scopeName(s))
			if len(cmds) == 0 {
				fmt.Println("  (none)")
			}
			for _, c := range cmds {
				fmt.Printf("  %s - %s\n", c.Command, c.Description)
			}
		}
		return nil
	}

	cmds := botcmd.Menu()
	for _, s := range menuScopes {
		if err := telegram.SetCommands(ctx, http.DefaultClient, token, cmds, s); err != nil {
			return fmt.Errorf("scope %s: %w", scopeName(s), err)
		}
	}
	names := make([]string, len(menuScopes))
	for i, s := range menuScopes {
		names[i] = scopeName(s)
	}
	fmt.Printf("pushed %d commands to: %s\n", len(cmds), strings.Join(names, ", "))
	for _, c := range cmds {
		fmt.Printf("  %s - %s\n", c.Command, c.Description)
	}
	return nil
}
