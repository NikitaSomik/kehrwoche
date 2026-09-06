package telegram

import (
	"encoding/json"
	"testing"
)

func TestDecodeUpdate(t *testing.T) {
	// A realistic group-chat delivery, including fields the bot ignores — they
	// must not upset decoding.
	body := `{"update_id":42,"message":{"message_id":7,
		"from":{"id":987654321,"is_bot":false,"first_name":"Nikita","language_code":"ru"},
		"chat":{"id":-1001234567890,"type":"supergroup","title":"WG"},
		"date":1757000000,"text":"/etage",
		"entities":[{"type":"bot_command","offset":0,"length":6}]}}`

	var u Update
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.UpdateID != 42 {
		t.Errorf("UpdateID = %d, want 42", u.UpdateID)
	}
	if u.Message == nil {
		t.Fatal("Message is nil")
	}
	if got := u.Message.From.ID; got != 987654321 {
		t.Errorf("From.ID = %d", got)
	}
	if got := u.Message.Chat.ID; got != -1001234567890 {
		t.Errorf("Chat.ID = %d", got)
	}
	if u.Message.Chat.IsPrivate() {
		t.Error("a supergroup must not read as private")
	}
	if got := u.Message.Command(); got != "etage" {
		t.Errorf("Command() = %q, want etage", got)
	}
}

func TestUpdateWithoutMessage(t *testing.T) {
	// Edits, callbacks and channel posts arrive with no message field.
	var u Update
	if err := json.Unmarshal([]byte(`{"update_id":1}`), &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Message != nil {
		t.Error("Message should be nil")
	}
}

func msg(text string, entities ...MessageEntity) *Message {
	return &Message{Text: text, Entities: entities}
}

func botCommand(length int) MessageEntity {
	return MessageEntity{Type: "bot_command", Offset: 0, Length: length}
}

func TestCommand(t *testing.T) {
	cases := []struct {
		name string
		msg  *Message
		want string
	}{
		{"plain command", msg("/etage", botCommand(6)), "etage"},
		{"underscore variant", msg("/etage_plan", botCommand(11)), "etage_plan"},
		{"addressed to the bot in a group", msg("/etage@KehrwocheBot", botCommand(19)), "etage"},
		{"with arguments", msg("/etage bitte", botCommand(6)), "etage"},
		{"arguments in German", msg("/etage Grüße an alle", botCommand(6)), "etage"},

		{"slash only", msg("/", botCommand(1)), ""},
		{"separated by a newline", msg("/etage\nbitte", botCommand(6)), "etage"},
		{"separated by a carriage return", msg("/etage\r\nbitte", botCommand(6)), "etage"},

		{"no entities at all", msg("/etage"), ""},
		{"not marked as a command", msg("/etage", MessageEntity{Type: "italic", Offset: 0, Length: 6}), ""},
		{"command not at the start", msg("see /etage", MessageEntity{Type: "bot_command", Offset: 4, Length: 6}), ""},
		{"ordinary text", msg("etage"), ""},
		{"empty message", msg(""), ""},
		{"nil message", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.Command(); got != tc.want {
				t.Errorf("Command() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The library this replaces did `m.Text[1:entity.Length]` with no bounds
// check, so an entity claiming more characters than the text holds panicked
// with "slice bounds out of range". Nothing here indexes by that number.
func TestCommandIgnoresAnImpossibleEntityLength(t *testing.T) {
	for _, length := range []int{9999, -1, 0} {
		got := msg("/a", botCommand(length)).Command()
		if got != "a" {
			t.Errorf("length %d: Command() = %q, want a", length, got)
		}
	}
}

// The Bot API documents no ordering for the entities array, so a command must
// be found wherever it sits in it.
func TestCommandFindsAnEntityThatIsNotFirst(t *testing.T) {
	m := msg("/etage @nikita",
		MessageEntity{Type: "mention", Offset: 7, Length: 7},
		botCommand(6),
	)
	if got := m.Command(); got != "etage" {
		t.Errorf("Command() = %q, want etage", got)
	}
}

func TestIsPrivate(t *testing.T) {
	cases := []struct {
		chat *Chat
		want bool
	}{
		{&Chat{Type: "private"}, true},
		{&Chat{Type: "group"}, false},
		{&Chat{Type: "supergroup"}, false},
		{&Chat{}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := tc.chat.IsPrivate(); got != tc.want {
			t.Errorf("%+v: got %v, want %v", tc.chat, got, tc.want)
		}
	}
}
