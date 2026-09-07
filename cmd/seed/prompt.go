package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// asker fills in what the caller left off the command line, and draws the run
// as one framed block: an intro, a question per line hanging off the rail, and
// an outro. The shape is @clack/prompts', by way of Laravel Prompts.
//
// Flags always win: only a value that wasn't given is asked for, which is how
// -vacant has always behaved. And it is only asked when there is somebody to
// answer — piping or redirecting stdin turns every prompt back into its
// default, so a script keeps behaving like a script.
type asker struct {
	in  *bufio.Reader
	out io.Writer
	// tty is the terminal to put into raw mode for a list prompt. nil when
	// there isn't one — tests, and any run that doesn't own a terminal.
	tty         *os.File
	interactive bool
	// redraw reports whether cursor motion reaches a terminal. Collapsing a
	// finished prompt into its answer means moving the cursor back over it, so
	// a redirected stdout gets the plain, additive form instead.
	redraw bool
	st     style
}

// newAsker reads the terminal, if this is one. Deciding once here means the
// prompts don't each have to wonder.
func newAsker() *asker {
	return &asker{
		in:          bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		tty:         os.Stdin,
		interactive: isTerminal(os.Stdin),
		redraw:      isTerminal(os.Stdout),
		st:          newStyle(),
	}
}

// isTerminal reports whether f is a character device — a terminal — rather
// than a pipe or a file. os.Stat is enough for this; no dependency needed.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// --- the frame -------------------------------------------------------------

func (a *asker) printf(format string, args ...any) {
	fmt.Fprintf(a.out, format, args...)
}

// intro opens the frame. Nothing is drawn for a run nobody is watching.
func (a *asker) intro(title string) {
	if !a.interactive {
		return
	}
	a.printf("%s  %s\n%s\n", a.st.done(symIntro), a.st.current(title), a.st.rail(symBar))
}

// outro closes it.
func (a *asker) outro(msg string) {
	if !a.interactive {
		fmt.Fprintln(a.out, "seed:", msg)
		return
	}
	// No leading rail here: every block that can precede this one — an
	// answer, the totals, the confirmation — already ends with it.
	a.printf("%s  %s\n", a.st.done(symOutro), msg)
}

// cancelled closes it on the unhappy path, in a colour that says so.
func (a *asker) cancelled(msg string) {
	if !a.interactive {
		fmt.Fprintln(a.out, "seed:", msg)
		return
	}
	a.printf("%s  %s\n", a.st.danger(symStop), a.st.warning(msg))
}

// step draws the header of a question: the symbol, the label, and a dimmed
// hint beside it.
func (a *asker) step(sym, question, hint string) {
	a.printf("%s  %s", sym, a.st.current(question))
	if hint != "" {
		a.printf("  %s", a.st.rail(hint))
	}
	a.printf("\n")
}

// rewind moves the cursor back over n lines already drawn and clears them.
// Without a terminal there is no cursor to move, so nothing is written and the
// output simply accumulates — which is also what makes the prompts testable.
//
// The \r comes first: the cursor may be part-way along a line the terminal
// echoed, and clearing from there would leave its left half behind.
func (a *asker) rewind(n int) {
	if !a.redraw || n <= 0 {
		return
	}
	a.printf("\r%s%s", ansiUp(n), ansiClearDown)
}

// answered collapses a finished question into the answer it got. With a
// terminal it rewinds over the prompt and redraws; without one it just adds
// the line, because there is no cursor to move.
func (a *asker) answered(question, answer string, drawn int) {
	a.rewind(drawn)
	a.step(a.st.done(symDone), question, "")
	if answer == "" {
		answer = "—"
	}
	a.printf("%s  %s\n%s\n", a.st.rail(symBar), a.st.value(answer), a.st.rail(symBar))
}

// --- text ------------------------------------------------------------------

// line asks for a string, returning def when the answer is empty. def is also
// what comes back when nobody is there to ask.
func (a *asker) line(question, def string) string {
	if !a.interactive {
		return def
	}
	hint := ""
	if def != "" {
		hint = "(" + def + ")"
	}
	a.step(a.st.active(symActive), question, hint)
	a.printf("%s  %s ", a.st.rail(symBar), a.st.rail(markHere))

	answer, err := a.in.ReadString('\n')
	if err != nil && answer == "" {
		// stdin closed mid-session; treat it as accepting the default rather
		// than looping on an input that will never arrive. Nothing echoed the
		// newline, so the cursor is still on the line the prompt is on.
		a.answered(question, def, 1)
		return def
	}
	if answer = strings.TrimSpace(answer); answer == "" {
		answer = def
	}
	// The step line and the line the answer was typed on; the terminal echoed
	// the newline, so the cursor already sits below both.
	a.answered(question, answer, 2)
	return answer
}

// intVal asks for a number, repeating the question until one arrives.
func (a *asker) intVal(question string, def int) int {
	for {
		answer := a.line(question, strconv.Itoa(def))
		n, err := strconv.Atoi(answer)
		if err == nil {
			return n
		}
		if !a.interactive {
			return def
		}
		a.printf("%s  %s\n%s\n",
			a.st.warning(symWarn),
			a.st.warning(fmt.Sprintf("%q is not a number", answer)),
			a.st.rail(symBar))
	}
}

// requireValue is line() for something the run cannot proceed without: with
// nobody to ask, it is an error rather than a silent default.
func (a *asker) requireValue(question, flagName string) (string, error) {
	if !a.interactive {
		return "", fmt.Errorf("%s is required (no terminal to ask on)", flagName)
	}
	if answer := a.line(question, ""); answer != "" {
		return answer, nil
	}
	return "", fmt.Errorf("%s is required", flagName)
}

// confirm asks a yes/no question. Anything but an explicit yes is a no, so a
// stray newline can't approve a write to the production database — which is
// also why this stays typed rather than becoming a two-way toggle with a
// highlighted default.
func (a *asker) confirm(question string) bool {
	if !a.interactive {
		// Non-interactive runs are scripts that already said what they wanted
		// on the command line; blocking them on a prompt nobody sees would
		// hang instead of protecting anything.
		return true
	}
	a.step(a.st.warning(symWarn), a.st.warning(question), "")
	a.printf("%s  %s %s ", a.st.rail(symBar), a.st.rail("[y/N]"), a.st.rail(markHere))

	answer, _ := a.in.ReadString('\n')
	yes := false
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		yes = true
	}
	a.printf("%s\n", a.st.rail(symBar))
	return yes
}

// --- list ------------------------------------------------------------------

// crlfWriter ends every line with a carriage return as well as a newline.
//
// Raw mode exists to deliver key presses unbuffered, but it also switches off
// ONLCR — the terminal driver's habit of turning \n into \r\n on the way out.
// Without that, a bare newline drops a line without returning to column 0 and
// the list walks off the right of the screen a step at a time.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	if _, err := c.w.Write(bytes.ReplaceAll(p, []byte("\n"), []byte("\r\n"))); err != nil {
		return 0, err
	}
	// Report the caller's own length: the translation is none of its business,
	// and a short write would look like an error to it.
	return len(p), nil
}

// choice is one row of a list. key is what the same answer would be typed as
// when there is no terminal to navigate with, so the list and its fallback
// speak the same language: room numbers for rooms, duty names for duties.
type choice struct {
	key   string
	label string
	hint  string
}

const listHint = "↑↓ move · space toggle · enter confirm"

// multiselect asks which of opts apply, with on carrying both the initial
// selection and the answer. It navigates with the arrow keys where it can and
// falls back to a typed, comma-separated list of keys where it can't — a
// redirected stdout, or a terminal that won't go into raw mode.
func (a *asker) multiselect(question string, opts []choice, on []bool) ([]bool, error) {
	if !a.interactive || len(opts) == 0 {
		return on, nil
	}
	if a.tty != nil && a.redraw {
		restore, err := rawMode(a.tty)
		if err == nil {
			defer restore()
			// Raw mode turns the terminal's own newline translation off, so
			// everything printed until restore has to carry the carriage
			// return itself.
			plain := a.out
			a.out = crlfWriter{plain}
			defer func() { a.out = plain }()
			return a.navigate(question, opts, on)
		}
	}
	return a.typeKeys(question, opts, on)
}

// navigate is the interactive half of multiselect: it owns the terminal, so it
// can repaint the list under the cursor on every key press.
func (a *asker) navigate(question string, opts []choice, on []bool) ([]bool, error) {
	cur := 0
	drawn := a.paintList(question, opts, on, cur)

	for {
		k, err := readKey(a.in)
		if err != nil {
			// The input ended without an answer. Keep what was selected
			// rather than inventing one.
			break
		}
		switch k.kind {
		case keyInterrupt:
			a.rewind(drawn)
			return nil, errCancelled
		case keyEnter:
			a.answered(question, summarise(opts, on), drawn)
			return on, nil
		case keyUp:
			cur = (cur - 1 + len(opts)) % len(opts)
		case keyDown:
			cur = (cur + 1) % len(opts)
		case keySpace:
			on[cur] = !on[cur]
		case keyRune:
			if k.r == 'a' || k.r == 'A' {
				all := !allTrue(on)
				for i := range on {
					on[i] = all
				}
			}
		}
		a.rewind(drawn)
		drawn = a.paintList(question, opts, on, cur)
	}
	a.answered(question, summarise(opts, on), drawn)
	return on, nil
}

// paintList draws the list and reports how many lines it took, which is how
// far back the cursor has to go to redraw it.
func (a *asker) paintList(question string, opts []choice, on []bool, cur int) int {
	a.step(a.st.active(symActive), question, listHint)
	width := 0
	for _, o := range opts {
		if n := len([]rune(o.label)); n > width {
			width = n
		}
	}
	for i, o := range opts {
		mark, here := markOff, "  "
		if on[i] {
			mark = markOn
		}
		if i == cur {
			here = a.st.value(markHere) + " "
		}
		// Only a row with something to its right needs padding, and it is
		// padded before colouring: the escape bytes count towards a %-*s
		// width and would leave the hint column ragged.
		label := o.label
		if o.hint != "" {
			label = fmt.Sprintf("%-*s", width, o.label)
		}
		if on[i] {
			label = a.st.done(label)
		} else if i == cur {
			label = a.st.current(label)
		}
		a.printf("%s  %s%s %s", a.st.rail(symBar), here, mark, label)
		if o.hint != "" {
			a.printf("  %s", a.st.rail(o.hint))
		}
		a.printf("\n")
	}
	return 1 + len(opts)
}

// typeKeys is multiselect without a terminal to navigate: the same question,
// answered by typing the keys, which is how -duty and -vacant read on the
// command line anyway.
func (a *asker) typeKeys(question string, opts []choice, on []bool) ([]bool, error) {
	keys := make([]string, len(opts))
	for i, o := range opts {
		keys[i] = o.key
	}
	answer := a.line(question, "")
	if answer == "" {
		return on, nil
	}

	next := make([]bool, len(on))
	for _, part := range strings.Split(answer, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := indexOf(keys, part)
		if i < 0 {
			return nil, fmt.Errorf("%q is not one of: %s", part, strings.Join(keys, ", "))
		}
		next[i] = true
	}
	return next, nil
}

func summarise(opts []choice, on []bool) string {
	var picked []string
	for i, o := range opts {
		if on[i] {
			picked = append(picked, o.label)
		}
	}
	return strings.Join(picked, ", ")
}

func allTrue(on []bool) bool {
	for _, v := range on {
		if !v {
			return false
		}
	}
	return len(on) > 0
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

// stop closes the frame for an answer that ended the run. Ctrl+C is a decision,
// not a failure, so it is drawn here rather than printed to stderr; every other
// error travels on untouched and is reported by main.
func (a *asker) stop(err error) error {
	if errors.Is(err, errCancelled) {
		a.cancelled("cancelled, nothing written")
	}
	return err
}
