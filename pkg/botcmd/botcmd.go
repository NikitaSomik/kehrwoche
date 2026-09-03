// Package botcmd defines the bot's slash commands: the duty commands (each a
// menu name, a German description, and a handler) and the static /help and
// /start replies. api/webhook.go dispatches to it; cmd/setcommands pushes the
// menu to Telegram.
//
// It lives here rather than in api/ because Vercel's Go runtime treats every
// non-test file under api/ as a separate serverless function and requires each
// to export an HTTP handler.
package botcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
	"github.com/nikitasomusev/kehrwoche/pkg/telegram"
)

// Handler answers one command, given a DB accessor and the current time.
type Handler func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error)

// command couples a menu name, its German description (shown in Telegram's
// command menu and /help), and its handler — the single source of truth.
type command struct {
	name    string
	desc    string
	handler Handler
}

// duties: keep each wer/plan pair together and in this order — /help groups by
// the pair, and cmd/setcommands pushes them to Telegram in this order.
var duties = []command{
	{"toilette1", "Wer putzt diese Woche die Toilette 1?", wer(schedule.DutyTypeToilet1)},
	{"toilette1_plan", "Plan für die nächsten 4 Wochen (Toilette 1)", plan(schedule.DutyTypeToilet1)},
	{"toilette2", "Wer putzt diese Woche die Toilette 2?", wer(schedule.DutyTypeToilet2)},
	{"toilette2_plan", "Plan für die nächsten 4 Wochen (Toilette 2)", plan(schedule.DutyTypeToilet2)},
	{"treppenhaus", "Wer putzt diese Woche das Treppenhaus?", wer(schedule.DutyTypeHall)},
	{"treppenhaus_plan", "Plan für die nächsten 4 Wochen (Treppenhaus)", plan(schedule.DutyTypeHall)},
	{"etage", "Wer putzt diese Woche die Etage?", wer(schedule.DutyTypeFloor)},
	{"etage_plan", "Plan für die nächsten 4 Wochen (Etage)", plan(schedule.DutyTypeFloor)},
	{"waschkueche", "Wer putzt diese Woche die Waschküche?", wer(schedule.DutyTypeLaundry)},
	{"waschkueche_plan", "Plan für die nächsten 4 Wochen (Waschküche)", plan(schedule.DutyTypeLaundry)},
}

var handlers = func() map[string]Handler {
	m := make(map[string]Handler, len(duties))
	for _, c := range duties {
		m[c.name] = c.handler
	}
	return m
}()

// Lookup returns the handler for a duty command, or ok=false for anything not
// in the list (the webhook then stays silent).
func Lookup(name string) (Handler, bool) {
	h, ok := handlers[name]
	return h, ok
}

// Menu is the setMyCommands list in menu order: the duty commands, then /help.
func Menu() []telegram.Command {
	out := make([]telegram.Command, 0, len(duties)+1)
	for _, c := range duties {
		out = append(out, telegram.Command{Command: c.name, Description: c.desc})
	}
	return append(out, telegram.Command{Command: "help", Description: "Alle Befehle anzeigen"})
}

const startMessage = "Hallo! Ich erinnere die WG daran, wer mit Putzen dran ist.\n\n/help zeigt alle Befehle."

// helpMessage is built from duties so it can't drift from what the bot serves.
// Sent with telegram.SendPlain (no Markdown) — the _plan command names contain
// underscores that legacy Markdown reads as italics.
var helpMessage = buildHelpMessage()

func buildHelpMessage() string {
	var b strings.Builder
	b.WriteString("Kehrwoche-Bot – wer ist diese Woche mit Putzen dran?\n")
	for _, c := range duties {
		if !strings.HasSuffix(c.name, "_plan") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "/%s — %s\n", c.name, c.desc)
	}
	b.WriteString("\nJeden Donnerstag gegen 12 Uhr meldet der Bot hier automatisch, wer dran ist.")
	return b.String()
}

// StaticReply returns the canned text for /help (any chat) and /start (private
// chat only — Telegram auto-sends /start when a user first opens the bot). ok
// is false for anything else, including /start in a group, so those fall
// through to Lookup (and, if unknown, to a silent return).
func StaticReply(cmd string, private bool) (string, bool) {
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

func wer(dutyType schedule.DutyType) Handler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error) {
		result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
		if err != nil {
			return "", err
		}
		return result.Format(dutyType.Label(), dutyType.Window(now)), nil
	}
}

func plan(dutyType schedule.DutyType) Handler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (string, error) {
		entries, err := schedule.GetUpcoming(ctx, conn, dutyType, now, dutyType.PlanCount())
		if err != nil {
			return "", err
		}
		lines := make([]string, len(entries))
		for i, e := range entries {
			room := e.Room
			if room == "" {
				room = "—"
			}
			lines[i] = fmt.Sprintf("%s: %s", dutyType.Window(e.Date), room)
		}
		return fmt.Sprintf("🗓️ *%s — nächste %d Wochen:*\n\n%s", dutyType.Label(), schedule.PlanWeeks, strings.Join(lines, "\n")), nil
	}
}
