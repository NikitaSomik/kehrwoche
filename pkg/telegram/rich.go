package telegram

import (
	"context"
	"errors"
	"html"
	"log"
	"net/http"
	"strings"
)

// Rich is a message sent with sendRichMessage (Bot API 10.1): HTML is the
// rich_message's html content, which unlike the legacy parse modes has
// headings and tables, and Plain is the same content as plain text.
//
// Plain is what lands if Telegram refuses the rich message: a group that
// can't receive one, or a markup error. A refusal (*APIError) proves nothing
// was sent, so the plain copy can't duplicate it.
type Rich struct {
	HTML  string
	Plain string
}

// SendRich posts m to chatID, or m.Plain when Telegram refuses the rich
// message as malformed or unknown (400 or 404). The refusal is logged, since
// the plain copy hides it from the caller.
func (c *Client) SendRich(ctx context.Context, chatID int64, m Rich) error {
	err := c.call(ctx, "sendRichMessage", map[string]any{
		"chat_id":      chatID,
		"rich_message": map[string]any{"html": m.HTML},
	}, nil)
	if err == nil || m.Plain == "" || !richRefused(err) {
		return err
	}
	log.Printf("%v; sending plain text instead", err)
	return c.SendPlain(ctx, chatID, m.Plain)
}

func richRefused(err error) bool {
	apiErr, ok := errors.AsType[*APIError](err)
	return ok && (apiErr.Code == http.StatusBadRequest || apiErr.Code == http.StatusNotFound)
}

// Title renders a message's title as a bold line: a heading tag renders in a
// large serif face that dwarfs the short messages it heads.
func Title(text string) string {
	return "<p><b>" + Esc(text) + "</b></p>"
}

// Esc escapes text for a rich message's html.
func Esc(s string) string {
	return html.EscapeString(s)
}

// NoHighlight is HTMLTable's highlight for a table with no row in bold.
const NoHighlight = -1

// HTMLTable renders a compact, striped table; every cell is escaped.
// Telegram's default cell padding makes a short table fill the screen, hence
// compact. A nil header leaves out the header row, for a table whose columns
// speak for themselves; header cells are left-aligned like the rest, where
// Telegram would centre them. The row at index highlight is set in bold, e.g.
// the current week of a plan; NoHighlight sets none.
func HTMLTable(header []string, rows [][]string, highlight int) string {
	var b strings.Builder
	b.WriteString("<table compact striped>")
	if header != nil {
		b.WriteString("<tr>")
		for _, h := range header {
			b.WriteString(`<th align="left">` + Esc(h) + "</th>")
		}
		b.WriteString("</tr>")
	}
	for i, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			if i == highlight {
				b.WriteString("<td><b>" + Esc(cell) + "</b></td>")
			} else {
				b.WriteString("<td>" + Esc(cell) + "</td>")
			}
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}
