// Command setcommands sets the command menu for the WG group (CHAT_ID), /start
// alone for all private chats, and deletes the menu from the other broad
// scopes (default, all group chats, all chat administrators), so strangers see
// nothing but /start. Telegram's Start button shows only in a fresh chat, so
// the private menu keeps /start one tap away for someone who wrote before.
// Group admins get the full menu in their own private chat when they send
// /start (api/webhook.go), a chat scope that overrides the private one. It hits
// the Telegram API — run it by hand.
//
//	task setcommands           # menu for the group, /start in private, none elsewhere
//	task setcommands -- -show   # print Telegram's current menus, change nothing
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/NikitaSomik/kehrwoche/pkg/botcmd"
	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

// menuScopes: "" is the default scope; the rest are the BotCommandScope types
// BotFather shows as toggles.
var menuScopes = []string{"", privateScope, "all_group_chats", "all_chat_administrators"}

const privateScope = "all_private_chats"

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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.TelegramToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}
	bot := telegram.New(cfg.TelegramToken)
	groupID := cfg.ChatID
	if groupID == 0 {
		return fmt.Errorf("CHAT_ID is not set")
	}

	if show {
		for _, s := range menuScopes {
			cmds, err := bot.GetCommands(ctx, s)
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
	if err := bot.SetChatCommands(ctx, cmds, groupID); err != nil {
		return fmt.Errorf("group: %w", err)
	}
	if err := bot.SetCommands(ctx, botcmd.PrivateMenu(), privateScope); err != nil {
		return fmt.Errorf("scope %s: %w", privateScope, err)
	}
	var names []string
	for _, s := range menuScopes {
		if s == privateScope {
			continue
		}
		if err := bot.DeleteCommands(ctx, s); err != nil {
			return fmt.Errorf("scope %s: %w", scopeName(s), err)
		}
		names = append(names, scopeName(s))
	}
	fmt.Printf("pushed %d commands to the group %d\n", len(cmds), groupID)
	fmt.Println("private chats: /start only; group admins get the full menu there with /start")
	fmt.Printf("removed the menu from: %s\n", strings.Join(names, ", "))
	return nil
}
