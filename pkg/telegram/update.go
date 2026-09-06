package telegram

import (
	"strings"
	"unicode"
)

// The types below are the slice of Telegram's webhook payload this bot acts
// on. The Bot API sends a great deal more with every update; encoding/json
// drops whatever isn't named here, so the cost of ignoring it is nil.
//
// They replace github.com/go-telegram-bot-api/telegram-bot-api, which had gone
// five years without a release while sitting on the one path untrusted input
// takes into the bot. Only three of its structs and two of its methods were
// ever used.

// Update is one webhook delivery. Message is nil for the update kinds the bot
// doesn't handle (edits, callbacks, channel posts).
type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int             `json:"message_id"`
	From      *User           `json:"from"`
	Chat      *Chat           `json:"chat"`
	Text      string          `json:"text"`
	Entities  []MessageEntity `json:"entities"`
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// MessageEntity is Telegram's markup for a span of the message text: which
// part is a command, a mention, a link.
//
// Offset and Length are counted in UTF-16 code units, while Go indexes strings
// by byte, so the two disagree for any text outside ASCII. Nothing here slices
// by them; Length is kept only because it is part of the payload, and reading
// it would be a mistake.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// entityBotCommand is the MessageEntity type Telegram uses to mark a command.
const entityBotCommand = "bot_command"

// IsPrivate reports whether the chat is a one-to-one conversation with the bot
// rather than a group. The other types Telegram defines are "group",
// "supergroup" and "channel". Safe on a nil receiver.
func (c *Chat) IsPrivate() bool {
	return c != nil && c.Type == "private"
}

// Command returns the bot command a message carries — without the leading
// slash, and without the @botname suffix Telegram appends in groups. It
// returns "" when the message isn't a command.
// Safe on a nil receiver, so a caller that has already checked Update.Message
// need not check again.
func (m *Message) Command() string {
	if !m.isCommand() {
		return ""
	}
	// Deliberately not m.Text[1:entity.Length]: the entity's length counts
	// UTF-16 units while Go slices bytes, so the two disagree the moment the
	// text isn't ASCII — and it arrives from outside as a number we would then
	// be indexing by. The end of the command is unambiguous in the text
	// itself, so read it from there and the question doesn't arise.
	cmd := m.Text[1:]
	if i := strings.IndexFunc(cmd, endsCommand); i != -1 {
		cmd = cmd[:i]
	}
	return cmd
}

// endsCommand reports whether r closes the command name: the @botname suffix
// Telegram appends in groups, or the whitespace before any arguments. Every
// space is treated alike rather than listing a few by hand, so a \r or a
// non-breaking space can't slip through into the looked-up name.
func endsCommand(r rune) bool {
	return r == '@' || unicode.IsSpace(r)
}

// isCommand reports whether Telegram marked the message as opening with a bot
// command. Text that merely begins with a slash is not one, which is why the
// entity is consulted at all.
func (m *Message) isCommand() bool {
	if m == nil || !strings.HasPrefix(m.Text, "/") {
		return false
	}
	// Search rather than read Entities[0]: the Bot API documents no ordering
	// for this array, so the command is not promised to arrive first.
	for _, e := range m.Entities {
		if e.Type == entityBotCommand && e.Offset == 0 {
			return true
		}
	}
	return false
}
