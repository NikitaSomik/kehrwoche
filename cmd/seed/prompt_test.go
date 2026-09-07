package main

import (
	"bufio"
	"errors"
	"io"
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

	// /dev/null is a character device, so a file-mode check calls it a
	// terminal — and `seed < /dev/null` gets prompted at instead of running
	// on its defaults.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = null.Close() })
	if isTerminal(null) {
		t.Error("/dev/null reported itself as a terminal")
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
		want := "Zimmer 1, Zimmer 2, Zimmer 3, Zimmer 4"
		if picked(opts, got) != want {
			t.Errorf("got %q, want %q", picked(opts, got), want)
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

// --- exclusive rows --------------------------------------------------------

func withExclusive() ([]choice, []bool) {
	opts := []choice{
		{key: "toilet1", label: "Toilette 1"},
		{key: "hall", label: "Treppenhaus", exclusive: true},
		{key: "floor", label: "Etage"},
	}
	return opts, make([]bool, 3)
}

// An exclusive row can't share the answer, and the list says so by clearing
// the other side rather than by refusing the key.
func TestToggleClearsTheOtherSide(t *testing.T) {
	t.Run("picking the exclusive row clears the rest", func(t *testing.T) {
		opts, on := withExclusive()
		on[0], on[2] = true, true

		toggle(opts, on, 1)
		if want := "Treppenhaus"; summarise(opts, on) != want {
			t.Errorf("got %q, want %q", summarise(opts, on), want)
		}
	})

	t.Run("picking anything else clears the exclusive row", func(t *testing.T) {
		opts, on := withExclusive()
		on[1] = true

		toggle(opts, on, 0)
		if want := "Toilette 1"; summarise(opts, on) != want {
			t.Errorf("got %q, want %q", summarise(opts, on), want)
		}
	})

	t.Run("the ordinary rows still stack", func(t *testing.T) {
		opts, on := withExclusive()

		toggle(opts, on, 0)
		toggle(opts, on, 2)
		if want := "Toilette 1, Etage"; summarise(opts, on) != want {
			t.Errorf("got %q, want %q", summarise(opts, on), want)
		}
	})

	// Turning a row off has nobody to clear, and must not resurrect anything.
	t.Run("clearing a row touches nothing else", func(t *testing.T) {
		opts, on := withExclusive()
		on[0], on[2] = true, true

		toggle(opts, on, 0)
		if want := "Etage"; summarise(opts, on) != want {
			t.Errorf("got %q, want %q", summarise(opts, on), want)
		}
	})
}

// "Everything" cannot mean an answer the list refuses, so the exclusive row is
// not part of it.
func TestSelectAllSkipsTheExclusiveRow(t *testing.T) {
	opts, on := withExclusive()

	selectAll(opts, on)
	if want := "Toilette 1, Etage"; summarise(opts, on) != want {
		t.Errorf("got %q, want %q", summarise(opts, on), want)
	}

	// Pressing it again clears the list rather than reaching for the one row
	// that was deliberately left out.
	selectAll(opts, on)
	if summarise(opts, on) != "" {
		t.Errorf("got %q, want nothing selected", summarise(opts, on))
	}
}

// Selecting all while the exclusive row holds the answer has to drop it, or
// the result would be the combination the list exists to prevent.
func TestSelectAllReplacesTheExclusiveRow(t *testing.T) {
	opts, on := withExclusive()
	on[1] = true

	selectAll(opts, on)
	if want := "Toilette 1, Etage"; summarise(opts, on) != want {
		t.Errorf("got %q, want %q", summarise(opts, on), want)
	}
}

// --- fitting the terminal ---------------------------------------------------

func TestFit(t *testing.T) {
	cases := []struct {
		name string
		text string
		n    int
		want string
	}{
		{"no limit", "Treppenhaus", 0, "Treppenhaus"},
		{"a negative limit is no limit", "Treppenhaus", -1, "Treppenhaus"},
		{"shorter than the limit", "Etage", 10, "Etage"},
		{"exactly the limit", "Etage", 5, "Etage"},
		{"longer, so it says so", "Treppenhaus", 6, "Trepp…"},
		{"one column left", "Treppenhaus", 1, "…"},
		{"runes, not bytes", "Waschküche", 6, "Wasch…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fit(tc.text, tc.n); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The label is what the answer is made of and the hint only explains it, so
// the hint gives way first — and goes entirely rather than be cut to a stub.
func TestFitPair(t *testing.T) {
	t.Run("both fit", func(t *testing.T) {
		label, hint := fitPair("Treppenhaus", "runs on its own", 40)
		if label != "Treppenhaus" || hint != "runs on its own" {
			t.Errorf("got %q / %q, want both untouched", label, hint)
		}
	})

	t.Run("the hint is trimmed before the label", func(t *testing.T) {
		label, hint := fitPair("Treppenhaus", "runs on its own", 26)
		if label != "Treppenhaus" {
			t.Errorf("label got cut while the hint still had room: %q", label)
		}
		if hint == "" || len([]rune(hint)) > 13 {
			t.Errorf("hint %q does not fit the 13 columns left", hint)
		}
	})

	t.Run("too little left for a hint drops it", func(t *testing.T) {
		label, hint := fitPair("Treppenhaus", "runs on its own", 16)
		if hint != "" {
			t.Errorf("got hint %q, want it dropped rather than cut to a stub", hint)
		}
		if label != "Treppenhaus" {
			t.Errorf("got label %q", label)
		}
	})

	t.Run("a label alone is cut when it has to be", func(t *testing.T) {
		label, _ := fitPair("Treppenhaus", "", 6)
		if label != "Trepp…" {
			t.Errorf("got %q", label)
		}
	})
}

func TestBudget(t *testing.T) {
	t.Run("an unknown width trims nothing", func(t *testing.T) {
		a := &asker{}
		if got := a.budget(rowPrefix); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	// One column is held back: a line that exactly fills the terminal wraps
	// on some of them.
	t.Run("a column is held back", func(t *testing.T) {
		a := &asker{width: 80}
		if got, want := a.budget(rowPrefix), 80-rowPrefix-1; got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	})

	t.Run("a terminal too narrow to hold anything still leaves one", func(t *testing.T) {
		a := &asker{width: 4}
		if got := a.budget(rowPrefix); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})
}

// The regression this all exists for: rewind moves the cursor back over the
// lines paintList says it drew, and the terminal moves it over the lines it
// displayed. A row wider than the terminal is one of the former and two of the
// latter, and the redraw then leaves debris and walks down the screen.
func TestPaintListNeverReachesTheEdge(t *testing.T) {
	const width = 44

	var out strings.Builder
	a := &asker{out: &out, interactive: true, width: width}
	opts := []choice{
		{label: "Toilette 1"},
		{label: "Treppenhaus", hint: "runs on its own, replaces a block"},
		{label: "Waschküche"},
	}
	on := make([]bool, len(opts))

	drawn := a.paintList("Duties to seed", opts, on, 1)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != drawn {
		t.Fatalf("drew %d lines but reported %d", len(lines), drawn)
	}
	for _, l := range lines {
		if n := len([]rune(l)); n >= width {
			t.Errorf("line reaches the edge at %d of %d columns: %q", n, width, l)
		}
	}
}

// With no width to fit to, nothing is trimmed — a redirected plan keeps its
// full text.
func TestPaintListLeavesTextAloneWithoutAWidth(t *testing.T) {
	var out strings.Builder
	a := &asker{out: &out, interactive: true}
	opts := []choice{{label: "Treppenhaus", hint: "runs on its own, replaces a block"}}

	a.paintList("Duties to seed", opts, make([]bool, 1), 0)

	if !strings.Contains(out.String(), "runs on its own, replaces a block") {
		t.Errorf("the hint was trimmed without a width to trim to:\n%s", out.String())
	}
}

// navigate edits its selection in place and typeKeys builds a fresh slice, so
// without a copy at the door the caller could not tell whether the slice it
// passed came back changed — and a cancelled prompt would hand back an error
// with that slice already overwritten behind it.
func TestMultiselectLeavesTheCallersSliceAlone(t *testing.T) {
	t.Run("the typed fallback", func(t *testing.T) {
		opts, on := fourRooms()
		on[0] = true
		a, _ := newTestAsker("2\n", true)

		got, err := a.multiselect("Vacant rooms", opts, on)
		if err != nil {
			t.Fatalf("multiselect: %v", err)
		}
		if !on[0] || on[1] {
			t.Errorf("the caller's selection changed: %v", on)
		}
		if want := "Zimmer 2"; picked(opts, got) != want {
			t.Errorf("got %q, want %q", picked(opts, got), want)
		}
	})

	t.Run("with nobody to ask", func(t *testing.T) {
		opts, on := fourRooms()
		on[0] = true
		a, _ := newTestAsker("", false)

		got, err := a.multiselect("Vacant rooms", opts, on)
		if err != nil {
			t.Fatalf("multiselect: %v", err)
		}
		// The answer is the same selection, but it must not be the same slice:
		// writing to what came back has to leave the caller's copy alone.
		got[3] = true
		if on[3] {
			t.Error("the answer aliases the caller's slice")
		}
		if !got[0] {
			t.Error("the default selection was lost")
		}
	})
}

// The branch that owns the terminal was verified by hand and by nothing else,
// which is how the missing carriage return got in: raw mode switches off the
// driver's own \n → \r\n translation, and a list drawn with bare newlines
// walks off the right of the screen a step at a time.
func TestMultiselectRawPathReturnsTheCarriage(t *testing.T) {
	var restored bool
	orig := enterRawMode
	enterRawMode = func(*os.File) (func(), error) {
		return func() { restored = true }, nil
	}
	t.Cleanup(func() { enterRawMode = orig })

	var out strings.Builder
	a := &asker{
		in:          bufio.NewReader(strings.NewReader("\x1b[B \r")),
		out:         &out,
		tty:         os.Stdin, // never touched: the fake above takes it
		interactive: true,
		redraw:      true,
	}
	opts, on := fourRooms()

	got, err := a.multiselect("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("multiselect: %v", err)
	}
	if want := "Zimmer 2"; picked(opts, got) != want {
		t.Errorf("got %q, want %q", picked(opts, got), want)
	}

	text := out.String()
	if strings.Count(text, "\n") == 0 {
		t.Fatal("nothing was drawn")
	}
	if n := strings.Count(text, "\n") - strings.Count(text, "\r\n"); n != 0 {
		t.Errorf("%d newline(s) went out without a carriage return", n)
	}
	if !restored {
		t.Error("the terminal was left in raw mode")
	}
	if a.out != io.Writer(&out) {
		t.Error("the CRLF writer outlived the raw mode it was installed for")
	}
}

// Without a frame around it, a closing line carries the command's name — which
// the prompts are told rather than know. Nothing else here is aware of which
// command it draws for.
func TestOutroNamesTheCommandOnlyWhenToldTo(t *testing.T) {
	t.Run("named", func(t *testing.T) {
		var out strings.Builder
		a := &asker{out: &out, name: "migrate"}

		a.outro("done")
		if got, want := out.String(), "migrate: done\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("unnamed", func(t *testing.T) {
		var out strings.Builder
		a := &asker{out: &out}

		a.cancelled("cancelled, nothing written")
		if got, want := out.String(), "cancelled, nothing written\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
