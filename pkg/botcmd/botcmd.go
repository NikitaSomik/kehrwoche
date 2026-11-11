// Package botcmd defines the bot's slash commands: two per duty (who is on
// this week, and the plan), built from the duties table, and the static /help and
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

// duty is one cleaning duty the bot answers for. Each one serves two
// commands: /<name> (who is on this week) and /<name>_plan (the coming weeks).
// Menu, Lookup and /help are all built from duties, so a duty added here is
// complete everywhere.
type duty struct {
	name string // command name, e.g. "etage"
	typ  schedule.DutyType
	what string // the duty with its article, for the menu: "die Etage"
}

// duties are in menu order: cmd/setcommands pushes them to Telegram this way.
var duties = []duty{
	{"toilette1", schedule.DutyTypeToilet1, "die Toilette 1"},
	{"toilette2", schedule.DutyTypeToilet2, "die Toilette 2"},
	{"treppenhaus", schedule.DutyTypeHall, "das Treppenhaus"},
	{"etage", schedule.DutyTypeFloor, "die Etage"},
	{"waschkueche", schedule.DutyTypeLaundry, "die Waschküche"},
}

const planSuffix = "_plan"

func (d duty) planName() string { return d.name + planSuffix }

// command is one menu entry: name, German description (shown in Telegram's
// command menu and /help) and handler.
type command struct {
	name    string
	desc    string
	handler Handler
}

// commands expands duties into the menu: each duty's command, then its plan.
var commands = func() []command {
	out := make([]command, 0, 2*len(duties))
	for _, d := range duties {
		out = append(out,
			command{d.name, fmt.Sprintf("Wer putzt diese Woche %s?", d.what), wer(d.typ)},
			command{d.planName(), fmt.Sprintf("Plan für die nächsten %d Wochen (%s)", schedule.PlanWeeks, d.typ.Label()), plan(d.typ)},
		)
	}
	return out
}()

var handlers = func() map[string]Handler {
	m := make(map[string]Handler, len(commands))
	for _, c := range commands {
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
	out := make([]telegram.Command, 0, len(commands)+1)
	for _, c := range commands {
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
	for i, c := range commands {
		if i%2 == 0 { // a duty's first command opens its group
			plain.WriteByte('\n')
		}
		fmt.Fprintf(&plain, "/%s — %s\n", c.name, c.desc)
	}
	for _, d := range duties {
		rows = append(rows, []string{d.typ.Label(), "/" + d.name, "/" + d.planName()})
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

// The two commands answered from canned text rather than from duties.
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
