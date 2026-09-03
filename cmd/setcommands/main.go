// Command setcommands pushes the bot's command menu to Telegram via
// setMyCommands, built from the same list the webhook serves (botcmd.Menu).
// It changes the bot's public config and hits the Telegram API — run it by hand.
//
//	task setcommands           # push the command menu
//	task setcommands -- -show   # print Telegram's current menu, change nothing
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/nikitasomusev/kehrwoche/pkg/botcmd"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

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
		cmds, err := telegram.GetCommands(ctx, http.DefaultClient, token)
		if err != nil {
			return err
		}
		for _, c := range cmds {
			fmt.Printf("%s - %s\n", c.Command, c.Description)
		}
		return nil
	}

	cmds := botcmd.Menu()
	if err := telegram.SetCommands(ctx, http.DefaultClient, token, cmds); err != nil {
		return err
	}
	fmt.Printf("pushed %d commands:\n", len(cmds))
	for _, c := range cmds {
		fmt.Printf("  %s - %s\n", c.Command, c.Description)
	}
	return nil
}
