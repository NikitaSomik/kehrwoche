package handler

import (
	"fmt"
	"strings"
	"testing"
)

func TestHelpMessageListsEveryCommand(t *testing.T) {
	for name := range commands {
		if !strings.Contains(helpMessage, "/"+name+" —") {
			t.Errorf("helpMessage has no entry for /%s", name)
		}
	}
}

func TestStaticCommand(t *testing.T) {
	cases := []struct {
		cmd     string
		private bool
		wantOK  bool
	}{
		{"help", true, true},
		{"help", false, true}, // /help works in the group too
		{"start", true, true},
		{"start", false, false}, // no /start noise in the group
		{"toilette1", true, false},
		{"unknown", true, false},
		{"", false, false},
	}
	for _, tc := range cases {
		reply, ok := staticCommand(tc.cmd, tc.private)
		if ok != tc.wantOK {
			t.Errorf("staticCommand(%q, private=%v): ok=%v, want %v", tc.cmd, tc.private, ok, tc.wantOK)
		}
		if ok && reply == "" {
			t.Errorf("staticCommand(%q, private=%v): ok but empty reply", tc.cmd, tc.private)
		}
	}
}

// TestMenuCommandsValidForTelegram checks the setMyCommands constraints and
// logs the list to paste into BotFather:
//
//	go test -run TestMenuCommandsValidForTelegram -v ./api/
func TestMenuCommandsValidForTelegram(t *testing.T) {
	var list strings.Builder
	for _, c := range MenuCommands() {
		if n := len(c.Command); n < 1 || n > 32 {
			t.Errorf("command %q: length %d, want 1–32", c.Command, n)
		}
		if strings.ToLower(c.Command) != c.Command {
			t.Errorf("command %q: must be lowercase", c.Command)
		}
		if n := len(c.Description); n < 1 || n > 256 {
			t.Errorf("/%s: description length %d, want 1–256", c.Command, n)
		}
		if strings.ContainsAny(c.Description, "\r\n") {
			t.Errorf("/%s: description contains a newline", c.Command)
		}
		fmt.Fprintf(&list, "%s - %s\n", c.Command, c.Description)
	}
	t.Logf("setMyCommands list:\n%s", list.String())
}
