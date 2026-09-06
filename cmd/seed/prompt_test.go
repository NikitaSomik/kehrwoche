package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func newTestAsker(input string, interactive bool) (*asker, *strings.Builder) {
	var out strings.Builder
	return &asker{
		in:          bufio.NewReader(strings.NewReader(input)),
		out:         &out,
		interactive: interactive,
	}, &out
}

func TestAskerLine(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		def         string
		interactive bool
		want        string
	}{
		{"answer is used", "laundry\n", "", true, "laundry"},
		{"whitespace is trimmed", "  laundry  \n", "", true, "laundry"},
		{"empty answer falls back to the default", "\n", "toilet1", true, "toilet1"},
		{"closed input falls back to the default", "", "toilet1", true, "toilet1"},
		{"nothing is asked without a terminal", "laundry\n", "toilet1", false, "toilet1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestAsker(tc.input, tc.interactive)
			if got := a.line("Duty", tc.def); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAskerLineAsksNothingWithoutATerminal(t *testing.T) {
	a, out := newTestAsker("laundry\n", false)
	a.line("Duty", "toilet1")
	if out.String() != "" {
		t.Errorf("printed %q; a script must not be shown prompts", out.String())
	}
}

func TestAskerIntVal(t *testing.T) {
	t.Run("re-asks until a number arrives", func(t *testing.T) {
		a, out := newTestAsker("soon\n-\n12\n", true)
		if got := a.intVal("Weeks", 26); got != 12 {
			t.Errorf("got %d, want 12", got)
		}
		if !strings.Contains(out.String(), "is not a number") {
			t.Errorf("the bad answers went unremarked:\n%s", out.String())
		}
	})

	t.Run("empty answer keeps the default", func(t *testing.T) {
		a, _ := newTestAsker("\n", true)
		if got := a.intVal("Weeks", 26); got != 26 {
			t.Errorf("got %d, want 26", got)
		}
	})

	t.Run("no terminal, no question", func(t *testing.T) {
		a, _ := newTestAsker("12\n", false)
		if got := a.intVal("Weeks", 26); got != 26 {
			t.Errorf("got %d, want the default 26", got)
		}
	})
}

// Only an explicit yes may approve a write to the production database.
func TestAskerConfirm(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"\n", false},
		{"maybe\n", false},
		{"", false},
	}
	for _, tc := range cases {
		a, _ := newTestAsker(tc.input, true)
		if got := a.confirm("Write?"); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.input, got, tc.want)
		}
	}
}

// A script has already said what it wants on the command line; stopping it on
// a prompt nobody can see would hang rather than protect anything.
func TestAskerConfirmProceedsWithoutATerminal(t *testing.T) {
	a, _ := newTestAsker("", false)
	if !a.confirm("Write?") {
		t.Error("a non-interactive run must not be blocked by the confirmation")
	}
}

func TestAskerRequireValue(t *testing.T) {
	t.Run("returns what was typed", func(t *testing.T) {
		a, _ := newTestAsker("2026-11-06\n", true)
		got, err := a.requireValue("Start date", "-start")
		if err != nil || got != "2026-11-06" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("an empty answer is an error", func(t *testing.T) {
		a, _ := newTestAsker("\n", true)
		if _, err := a.requireValue("Start date", "-start"); err == nil {
			t.Error("got nil, want an error naming the flag")
		}
	})

	// Silently defaulting here would seed from the wrong date.
	t.Run("no terminal is an error, not a default", func(t *testing.T) {
		a, _ := newTestAsker("", false)
		_, err := a.requireValue("Start date", "-start")
		if err == nil {
			t.Fatal("got nil, want an error")
		}
		if !strings.Contains(err.Error(), "-start") {
			t.Errorf("error should name the flag: %v", err)
		}
	})
}

// A pipe is not a terminal — the check that keeps prompts away from scripts.
func TestIsTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	if isTerminal(r) {
		t.Error("a pipe reported itself as a terminal")
	}

	f, err := os.CreateTemp(t.TempDir(), "seed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if isTerminal(f) {
		t.Error("a regular file reported itself as a terminal")
	}
}
