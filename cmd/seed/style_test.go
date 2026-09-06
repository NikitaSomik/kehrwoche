package main

import (
	"strings"
	"testing"
)

func TestStyleDisabledLeavesTextAlone(t *testing.T) {
	var plain style // zero value: colour off
	for _, got := range []string{
		plain.question("Weeks"), plain.hint("[26]"), plain.duty("toilet1"),
		plain.room("Zimmer 4"), plain.warning("careful"), plain.danger("production"),
	} {
		if strings.Contains(got, "\033") {
			t.Errorf("escape sequence leaked into plain output: %q", got)
		}
	}
	if got := plain.question("Weeks"); got != "Weeks" {
		t.Errorf("got %q, want the text unchanged", got)
	}
}

func TestStyleEnabledWrapsAndResets(t *testing.T) {
	colour := style{enabled: true}

	got := colour.question("Weeks")
	if !strings.HasPrefix(got, ansiBold) {
		t.Errorf("%q does not start with the bold sequence", got)
	}
	// Without the reset every line after this one would inherit the styling.
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("%q does not reset", got)
	}
}

// Wrapping "" would emit a sequence with nothing between it and the reset,
// which is noise in the output and in any diff of it.
func TestStyleLeavesEmptyStringsAlone(t *testing.T) {
	colour := style{enabled: true}
	if got := colour.hint(""); got != "" {
		t.Errorf("got %q, want an empty string", got)
	}
}

// The column is padded before it is coloured; the other way round the escape
// bytes count towards the width and the table goes crooked.
func TestColouredColumnKeepsItsVisibleWidth(t *testing.T) {
	colour := style{enabled: true}
	padded := colour.duty(pad12("toilet1"))

	visible := strings.NewReplacer(ansiCyan, "", ansiReset, "").Replace(padded)
	if len(visible) != 12 {
		t.Errorf("visible width is %d, want 12: %q", len(visible), visible)
	}
}

func pad12(s string) string {
	for len(s) < 12 {
		s += " "
	}
	return s
}
