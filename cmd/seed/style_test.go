package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
)

func TestStyleDisabledLeavesTextAlone(t *testing.T) {
	var plain style // zero value: colour off
	for _, got := range []string{
		plain.strong("Weeks"), plain.muted("[26]"), plain.accent("toilet1"),
		plain.success("Zimmer 4"), plain.warning("careful"), plain.danger("production"),
	} {
		if strings.Contains(got, "\033") {
			t.Errorf("escape sequence leaked into plain output: %q", got)
		}
	}
	if got := plain.strong("Weeks"); got != "Weeks" {
		t.Errorf("got %q, want the text unchanged", got)
	}
}

func TestStyleEnabledWrapsAndResets(t *testing.T) {
	colour := style{enabled: true}

	got := colour.strong("Weeks")
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
	if got := colour.muted(""); got != "" {
		t.Errorf("got %q, want an empty string", got)
	}
}

// The column is padded before it is coloured; the other way round the escape
// bytes count towards the width and the table goes crooked.
func TestColouredColumnKeepsItsVisibleWidth(t *testing.T) {
	colour := style{enabled: true}
	padded := colour.accent(pad12("toilet1"))

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

// A styled word inside a styled line used to end the outer colour early: the
// nested reset turned everything after "production" plain.
func TestStyleReopensAfterANestedReset(t *testing.T) {
	colour := style{enabled: true}
	got := colour.warning("write to " + colour.danger("production") + " now?")

	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("%q does not reset at the end", got)
	}
	// The tail after the inner reset must carry the warning colour again.
	tail := got[strings.LastIndex(got, ansiRed):]
	if !strings.Contains(tail, ansiWarn) {
		t.Errorf("the outer colour is not reopened after the nested one: %q", got)
	}
}

// The summary is the last thing on screen before the production database is
// written to, so its columns have to line up whether or not it is coloured.
func TestPrintTotalsFrameAndAlignment(t *testing.T) {
	var out strings.Builder
	printTotals(&out, style{}, []dutyTotal{
		{schedule.DutyTypeToilet1, 26, mustDate("2026-11-06"), mustDate("2027-04-30")},
		{schedule.DutyTypeLaundry, 52, mustDate("2026-11-03"), mustDate("2027-04-30")},
	})

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	want := []string{
		"├  plan",
		"│  Toilette 1    26  2026-11-06 → 2027-04-30",
		"│  Waschküche    52  2026-11-03 → 2027-04-30",
		"│  total         78",
		"│",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("got:\n%s\n\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// Nothing to report is not an empty frame — it is no frame at all.
func TestPrintTotalsSaysNothingWhenThereIsNothing(t *testing.T) {
	var out strings.Builder
	printTotals(&out, style{}, nil)
	if out.String() != "" {
		t.Errorf("printed %q, want nothing", out.String())
	}
}
