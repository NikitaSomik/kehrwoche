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

// Esc escapes text for a rich message's html.
func Esc(s string) string {
	return html.EscapeString(s)
}

// HTMLTable renders a table with a header row; every cell is escaped.
func HTMLTable(header []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("<table><tr>")
	for _, h := range header {
		b.WriteString("<th>" + Esc(h) + "</th>")
	}
	b.WriteString("</tr>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>" + Esc(cell) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}
