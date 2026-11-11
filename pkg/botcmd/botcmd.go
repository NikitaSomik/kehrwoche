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

	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
)

// Handler answers one command, given a DB accessor and the current time.
type Handler func(ctx context.Context, conn schedule.Querier, now time.Time) (telegram.Rich, error)

// command couples a menu name, its German description (shown in Telegram's
// command menu and /help), the duty it is about, and its handler — the single
// source of truth.
type command struct {
	name    string
	desc    string
	duty    schedule.DutyType
	handler Handler
}

// duties: keep each wer/plan pair together and in this order — /help groups by
// the pair, and cmd/setcommands pushes them to Telegram in this order.
var duties = []command{
	werCmd("toilette1", "Wer putzt diese Woche die Toilette 1?", schedule.DutyTypeToilet1),
	planCmd("toilette1_plan", "Plan für die nächsten 4 Wochen (Toilette 1)", schedule.DutyTypeToilet1),
	werCmd("toilette2", "Wer putzt diese Woche die Toilette 2?", schedule.DutyTypeToilet2),
	planCmd("toilette2_plan", "Plan für die nächsten 4 Wochen (Toilette 2)", schedule.DutyTypeToilet2),
	werCmd("treppenhaus", "Wer putzt diese Woche das Treppenhaus?", schedule.DutyTypeHall),
	planCmd("treppenhaus_plan", "Plan für die nächsten 4 Wochen (Treppenhaus)", schedule.DutyTypeHall),
	werCmd("etage", "Wer putzt diese Woche die Etage?", schedule.DutyTypeFloor),
	planCmd("etage_plan", "Plan für die nächsten 4 Wochen (Etage)", schedule.DutyTypeFloor),
	werCmd("waschkueche", "Wer putzt diese Woche die Waschküche?", schedule.DutyTypeLaundry),
	planCmd("waschkueche_plan", "Plan für die nächsten 4 Wochen (Waschküche)", schedule.DutyTypeLaundry),
}

func werCmd(name, desc string, d schedule.DutyType) command {
	return command{name: name, desc: desc, duty: d, handler: wer(d)}
}

func planCmd(name, desc string, d schedule.DutyType) command {
	return command{name: name, desc: desc, duty: d, handler: plan(d)}
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

// PrivateMenu is everyone's menu in private: Telegram's Start button shows
// only in a fresh chat, so this keeps /start one tap away for anyone who has
// written to the bot before. Admins' own chat menu overrides it.
func PrivateMenu() []telegram.Command {
	return []telegram.Command{{Command: CmdStart, Description: "Beitritt zur WG-Gruppe anfragen"}}
}

// JoinHint answers anything but /start from someone outside the group.
const JoinHint = "Tippe auf /start, um Zugang zur WG-Gruppe anzufragen."

const startMessage = "Hallo! Ich erinnere die WG daran, wer mit Putzen dran ist."

// helpMessage is built from duties so it can't drift from what the bot serves.
// Its plain copy is what the legacy Markdown made impossible to format: the
// _plan command names contain underscores it read as italics.
var helpMessage = buildHelpMessage()

func buildHelpMessage() telegram.Rich {
	var plain strings.Builder
	plain.WriteString(helpTitle + "\n")
	var rows [][]string
	for _, c := range duties {
		if !strings.HasSuffix(c.name, "_plan") {
			plain.WriteByte('\n')
			rows = append(rows, []string{c.duty.Label(), "/" + c.name, "/" + c.name + "_plan"})
		}
		fmt.Fprintf(&plain, "/%s — %s\n", c.name, c.desc)
	}
	cadence := fmt.Sprintf("Der Bot meldet sich hier automatisch %s gegen %d Uhr.",
		reminderDays(), schedule.ReminderHour)
	plain.WriteString("\n" + cadence)

	html := "<h3>" + telegram.Esc(helpTitle) + "</h3>" +
		telegram.HTMLTable([]string{"Bereich", "Diese Woche", fmt.Sprintf("Nächste %d Wochen", schedule.PlanWeeks)}, rows) +
		"<p>" + telegram.Esc(cadence) + "</p>"
	return telegram.Rich{HTML: html, Plain: plain.String()}
}

const helpTitle = "Kehrwoche-Bot – wer ist diese Woche mit Putzen dran?"

// reminderDays renders the announcement weekdays as German prose, e.g.
// "dienstags, donnerstags und freitags". Built from schedule.ReminderWeekdays
// so the sentence can't promise a cadence the cron doesn't keep — the previous
// hard-coded version claimed Thursdays only, and the wrong hour besides.
func reminderDays() string {
	days := schedule.ReminderWeekdays()
	words := make([]string, len(days))
	for i, d := range days {
		words[i] = schedule.GermanWeekdayAdverb(d)
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " und " + words[len(words)-1]
	}
}

// The two commands answered from canned text rather than the duties slice.
// Exported so the webhook can single out /start without repeating the literal.
const (
	CmdHelp  = "help"
	CmdStart = "start"
)

// StaticReply returns the canned text for /help (any chat) and /start (private
// chat only — Telegram auto-sends /start when a user first opens the bot). ok
// is false for anything else, including /start in a group, so those fall
// through to Lookup (and, if unknown, to a silent return).
func StaticReply(cmd string, private bool) (telegram.Rich, bool) {
	switch cmd {
	case CmdHelp:
		return helpMessage, true
	case CmdStart:
		if private {
			return telegram.Rich{HTML: "<p>" + telegram.Esc(startMessage) + "</p>", Plain: startMessage}, true
		}
	}
	return telegram.Rich{}, false
}

// AdminStartReply is the greeting followed by /help.
func AdminStartReply() telegram.Rich {
	return telegram.Rich{
		HTML:  "<p>" + telegram.Esc(startMessage) + "</p>" + helpMessage.HTML,
		Plain: startMessage + "\n\n" + helpMessage.Plain,
	}
}

// wer answers who is on duty this week, the room in bold.
func wer(dutyType schedule.DutyType) Handler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (telegram.Rich, error) {
		result, err := schedule.GetOnDuty(ctx, conn, dutyType, now)
		if err != nil {
			return telegram.Rich{}, err
		}
		head := fmt.Sprintf("%s (%s)", dutyType.Label(), dutyType.Window(now))
		if result.Room == "" {
			text := "❓ " + head + ": keine Planung."
			return telegram.Rich{HTML: "<p>" + telegram.Esc(text) + "</p>", Plain: text}, nil
		}
		return telegram.Rich{
			HTML:  "<p>🏠 " + telegram.Esc(head) + ": <b>" + telegram.Esc(result.Room) + "</b></p>",
			Plain: "🏠 " + head + ": " + result.Room,
		}, nil
	}
}

// plan answers the coming weeks as a table: window, room.
func plan(dutyType schedule.DutyType) Handler {
	return func(ctx context.Context, conn schedule.Querier, now time.Time) (telegram.Rich, error) {
		entries, err := schedule.GetUpcoming(ctx, conn, dutyType, now, dutyType.PlanCount())
		if err != nil {
			return telegram.Rich{}, err
		}
		rows := make([][]string, len(entries))
		lines := make([]string, len(entries))
		for i, e := range entries {
			room := e.Room
			if room == "" {
				room = "—"
			}
			window := dutyType.Window(e.Date)
			rows[i] = []string{window, room}
			lines[i] = window + ": " + room
		}
		title := fmt.Sprintf("🗓️ %s — nächste %d Wochen", dutyType.Label(), schedule.PlanWeeks)
		return telegram.Rich{
			HTML:  "<h3>" + telegram.Esc(title) + "</h3>" + telegram.HTMLTable([]string{"Zeitraum", "Zimmer"}, rows),
			Plain: title + ":\n\n" + strings.Join(lines, "\n"),
		}, nil
	}
}
