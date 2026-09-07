package main

import (
	"bufio"
	"errors"
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

// --- lists -----------------------------------------------------------------

func newListAsker(keys string) (*asker, *strings.Builder) {
	var out strings.Builder
	return &asker{
		in:          bufio.NewReader(strings.NewReader(keys)),
		out:         &out,
		interactive: true,
		// No terminal, so nothing rewinds and the frames simply accumulate.
		redraw: false,
	}, &out
}

func fourRooms() ([]choice, []bool) {
	opts := []choice{
		{key: "1", label: "Zimmer 1"},
		{key: "2", label: "Zimmer 2"},
		{key: "3", label: "Zimmer 3"},
		{key: "4", label: "Zimmer 4"},
	}
	return opts, make([]bool, 4)
}

func picked(opts []choice, on []bool) string {
	return summarise(opts, on)
}

func TestNavigateSelectsWithTheArrowKeys(t *testing.T) {
	opts, on := fourRooms()
	// down, space (2), down, down, space (4), enter
	a, _ := newListAsker("\x1b[B \x1b[B\x1b[B \r")

	got, err := a.navigate("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if want := "Zimmer 2, Zimmer 4"; picked(opts, got) != want {
		t.Errorf("got %q, want %q", picked(opts, got), want)
	}
}

// The cursor is a ring: up from the first row is the last one, which is how
// you reach the bottom of a list of eight rooms in one key press.
func TestNavigateWrapsAtTheEnds(t *testing.T) {
	opts, on := fourRooms()
	a, _ := newListAsker("\x1b[A \r") // up from the top, then select

	got, err := a.navigate("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if want := "Zimmer 4"; picked(opts, got) != want {
		t.Errorf("got %q, want %q", picked(opts, got), want)
	}
}

func TestNavigateTogglesEverythingWithA(t *testing.T) {
	t.Run("a selects all", func(t *testing.T) {
		opts, on := fourRooms()
		a, _ := newListAsker("a\r")
		got, _ := a.navigate("Vacant rooms", opts, on)
		if !allTrue(got) {
			t.Errorf("got %q, want every room", picked(opts, got))
		}
	})

	t.Run("a again clears them", func(t *testing.T) {
		opts, on := fourRooms()
		a, _ := newListAsker("aa\r")
		got, _ := a.navigate("Vacant rooms", opts, on)
		if picked(opts, got) != "" {
			t.Errorf("got %q, want nothing selected", picked(opts, got))
		}
	})
}

// Ctrl+C is a byte in raw mode, not a signal, so the list has to treat it as an
// answer that ends the run — and it must not come back as a selection.
func TestNavigateCancelsOnInterrupt(t *testing.T) {
	opts, on := fourRooms()
	a, _ := newListAsker(" \x03")

	got, err := a.navigate("Vacant rooms", opts, on)
	if !errors.Is(err, errCancelled) {
		t.Fatalf("got %v, want errCancelled", err)
	}
	if got != nil {
		t.Errorf("got a selection %v; a cancelled prompt has no answer", got)
	}
}

// An input that stops mid-list keeps what was selected rather than inventing
// an answer or spinning on a read that will never return.
func TestNavigateKeepsTheSelectionWhenInputEnds(t *testing.T) {
	opts, on := fourRooms()
	a, _ := newListAsker("\x1b[B ")

	got, err := a.navigate("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if want := "Zimmer 2"; picked(opts, got) != want {
		t.Errorf("got %q, want %q", picked(opts, got), want)
	}
}

func TestMultiselectAsksNothingWithoutATerminal(t *testing.T) {
	opts, on := fourRooms()
	on[0] = true
	a, out := newTestAsker("2,3\n", false)

	got, err := a.multiselect("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("multiselect: %v", err)
	}
	if want := "Zimmer 1"; picked(opts, got) != want {
		t.Errorf("got %q, want the untouched default %q", picked(opts, got), want)
	}
	if out.String() != "" {
		t.Errorf("printed %q; a script must not be shown prompts", out.String())
	}
}

// Without a terminal to navigate, the same question is answered by typing the
// keys — the same ones -vacant and -duty take on the command line.
func TestMultiselectFallsBackToTypedKeys(t *testing.T) {
	t.Run("keys are matched", func(t *testing.T) {
		opts, on := fourRooms()
		a, _ := newTestAsker("2, 4\n", true)

		got, err := a.multiselect("Vacant rooms", opts, on)
		if err != nil {
			t.Fatalf("multiselect: %v", err)
		}
		if want := "Zimmer 2, Zimmer 4"; picked(opts, got) != want {
			t.Errorf("got %q, want %q", picked(opts, got), want)
		}
	})

	t.Run("an empty answer keeps the default", func(t *testing.T) {
		opts, on := fourRooms()
		on[2] = true
		a, _ := newTestAsker("\n", true)

		got, err := a.multiselect("Vacant rooms", opts, on)
		if err != nil {
			t.Fatalf("multiselect: %v", err)
		}
		if want := "Zimmer 3"; picked(opts, got) != want {
			t.Errorf("got %q, want %q", picked(opts, got), want)
		}
	})

	t.Run("an unknown key is an error naming the valid ones", func(t *testing.T) {
		opts, on := fourRooms()
		a, _ := newTestAsker("9\n", true)

		_, err := a.multiselect("Vacant rooms", opts, on)
		if err == nil {
			t.Fatal("got nil, want an error")
		}
		if !strings.Contains(err.Error(), "1, 2, 3, 4") {
			t.Errorf("error should list the valid keys: %v", err)
		}
	})
}

// Raw mode switches off the terminal driver's own \n → \r\n translation, so a
// bare newline drops a line without returning to column 0 and the list walks
// off the right of the screen a step at a time.
func TestCRLFWriterReturnsTheCarriage(t *testing.T) {
	var out strings.Builder
	w := crlfWriter{&out}

	n, err := w.Write([]byte("one\ntwo\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := out.String(); got != "one\r\ntwo\r\n" {
		t.Errorf("got %q, want every newline paired with a return", got)
	}
	// io.Writer's contract is about the caller's bytes, not the translated
	// ones; a longer count reads as a short write to everything upstream.
	if n != len("one\ntwo\n") {
		t.Errorf("reported %d bytes written, want %d", n, len("one\ntwo\n"))
	}
}

func TestCRLFWriterLeavesAnExistingReturnAlone(t *testing.T) {
	var out strings.Builder
	if _, err := (crlfWriter{&out}).Write([]byte("\r\x1b[9A")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := out.String(); got != "\r\x1b[9A" {
		t.Errorf("got %q, want the rewind sequence untouched", got)
	}
}
