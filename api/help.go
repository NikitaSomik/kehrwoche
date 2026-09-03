package handler

import (
	"fmt"
	"strings"
)

// helpMessage is the /help reply, built from botCommands so it can't drift from
// the commands the bot actually serves. Sent with SendPlain (no Markdown) — the
// _plan command names contain underscores that legacy Markdown reads as italics.
var helpMessage = buildHelpMessage()

func buildHelpMessage() string {
	var b strings.Builder
	b.WriteString("Kehrwoche-Bot – wer ist diese Woche mit Putzen dran?\n")
	for _, c := range botCommands {
		if !strings.HasSuffix(c.name, "_plan") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "/%s — %s\n", c.name, c.desc)
	}
	b.WriteString("\nJeden Donnerstag gegen 12 Uhr meldet der Bot hier automatisch, wer dran ist.")
	return b.String()
}

const startMessage = "Hallo! Ich erinnere die WG daran, wer mit Putzen dran ist.\n\n/help zeigt alle Befehle."

// staticCommand returns the canned reply for /help (any chat) and /start
// (private chat only — Telegram auto-sends /start when a user first opens the
// bot). ok is false for anything else, including /start in a group, so those
// fall through to the duty-command map (and, if unknown, to a silent return).
func staticCommand(cmd string, private bool) (string, bool) {
	switch cmd {
	case "help":
		return helpMessage, true
	case "start":
		if private {
			return startMessage, true
		}
	}
	return "", false
}
