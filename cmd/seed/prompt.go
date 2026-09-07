package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
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
	// width is the terminal's, or 0 when unknown. Rewinding counts the lines
	// printed, and the terminal counts the lines displayed; a line too long to
	// fit becomes two of the latter and one of the former, and the redraw then
	// leaves debris behind and walks down the screen. Rather than predict the
	// wrap — Go can't measure a rune in columns, and some terminals draw ◼ and
	// ◆ double width — nothing is allowed to reach the edge in the first place.
	width int
	st    style
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
		width:       terminalWidth(os.Stdout),
		st:          newStyle(),
	}
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
	a.printf("%s  %s\n%s\n", a.st.success(symIntro), a.st.strong(title), a.st.muted(symBar))
}

// outro closes it.
func (a *asker) outro(msg string) {
	if !a.interactive {
		fmt.Fprintln(a.out, "seed:", msg)
		return
	}
	// No leading rail here: every block that can precede this one — an
	// answer, the totals, the confirmation — already ends with it.
	a.printf("%s  %s\n", a.st.success(symOutro), msg)
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
	a.printf("%s  %s", sym, a.st.strong(question))
	if hint != "" {
		a.printf("  %s", a.st.muted(hint))
	}
	a.printf("\n")
}

// The frame each kind of line carries before its text, in columns: the symbol
// and two spaces for a step, and the rail, cursor and mark for a list row.
const (
	stepPrefix = 3
	rowPrefix  = 7
)

// budget is how many columns are left for text after prefix columns of frame,
// or 0 when the width is unknown and nothing should be trimmed. One column is
// held back: a line that exactly fills the terminal wraps on some of them.
func (a *asker) budget(prefix int) int {
	if a.width <= 0 {
		return 0
	}
	if n := a.width - prefix - 1; n > 0 {
		return n
	}
	return 1
}

// fit trims text to at most n runes, saying so with an ellipsis. Runes, not
// columns — Go has no way to measure the latter — which is why the caller
// holds a column back and why the hint is dropped before the label is cut.
// n <= 0 means no limit.
func fit(text string, n int) string {
	if n <= 0 {
		return text
	}
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// fitPair trims a label and the hint beside it into one budget. The label is
// what the answer is made of and the hint only explains it, so the hint gives
// way first and disappears entirely rather than be cut to a stub.
func fitPair(label, hint string, budget int) (string, string) {
	if budget <= 0 {
		return label, hint
	}
	label = fit(label, budget)
	if hint == "" {
		return label, ""
	}
	rest := budget - len([]rune(label)) - 2
	if rest < 8 {
		return label, ""
	}
	return label, fit(hint, rest)
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
	a.step(a.st.success(symDone), question, "")
	if answer == "" {
		answer = "—"
	}
	a.printf("%s  %s\n%s\n", a.st.muted(symBar), a.st.accent(answer), a.st.muted(symBar))
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
	question, hint = fitPair(question, hint, a.budget(stepPrefix))
	a.step(a.st.accent(symActive), question, hint)
	a.printf("%s  %s ", a.st.muted(symBar), a.st.muted(markHere))

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
			a.st.muted(symBar))
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
	a.printf("%s  %s %s ", a.st.muted(symBar), a.st.muted("[y/N]"), a.st.muted(markHere))

	answer, _ := a.in.ReadString('\n')
	yes := false
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		yes = true
	}
	a.printf("%s\n", a.st.muted(symBar))
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
	out := bytes.ReplaceAll(p, []byte("\n"), []byte("\r\n"))
	n, err := c.w.Write(out)
	if err != nil {
		return 0, err
	}
	if n < len(out) {
		return 0, io.ErrShortWrite
	}
	// Report the caller's own length, not the translated one: the translation
	// is none of its business, and a longer count reads as a bug upstream.
	return len(p), nil
}

// choice is one row of a list. key is what the same answer would be typed as
// when there is no terminal to navigate with, so the list and its fallback
// speak the same language: room numbers for rooms, duty names for duties.
type choice struct {
	key   string
	label string
	hint  string
	// exclusive marks a row that cannot share the answer: selecting it clears
	// everything else, and selecting anything else clears it. It shapes the
	// live list only — the rule it stands for is the caller's, and the caller
	// still has to enforce it on the answers this list can't police (a typed
	// fallback, or a flag that skipped the question altogether).
	exclusive bool
}

// listHint names every key the list answers to, including the one nothing
// else would reveal. It is kept short on purpose: the hint is the first
// thing fitPair gives up, so a longer one would document the "a" key only
// on a wide terminal.
const listHint = "↑↓ · space · a all · enter"

// multiselect asks which of opts apply, with on carrying both the initial
// selection and the answer. It navigates with the arrow keys where it can and
// falls back to a typed, comma-separated list of keys where it can't — a
// redirected stdout, or a terminal that won't go into raw mode.
func (a *asker) multiselect(question string, opts []choice, on []bool) ([]bool, error) {
	// Work on a copy. navigate edits its selection in place and typeKeys
	// builds a fresh slice, so without this the caller has no way to know
	// whether the slice it passed came back changed — and a cancelled prompt
	// would hand back an error with the caller's own selection already
	// overwritten behind it.
	on = slices.Clone(on)
	if !a.interactive || len(opts) == 0 {
		return on, nil
	}
	if a.tty != nil && a.redraw {
		restore, err := enterRawMode(a.tty)
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

// enterRawMode is rawMode behind a variable, so a test can drive the branch
// that owns the terminal without owning one. Everything that branch does
// afterwards — the CRLF translation, the repaints, the rewinds — is ordinary
// code, and this is the only thing standing between it and a test.
var enterRawMode = rawMode

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
			toggle(opts, on, cur)
		case keyRune:
			// Lower case only. The tail of an escape sequence that arrived in
			// pieces reaches here as 'A', and selecting everything is not what
			// somebody pressing the up arrow meant.
			if k.r == 'a' {
				selectAll(opts, on)
			}
		}
		a.rewind(drawn)
		drawn = a.paintList(question, opts, on, cur)
	}
	a.answered(question, summarise(opts, on), drawn)
	return on, nil
}

// paintList draws the list and reports how many lines it took, which is how
// far back the cursor has to go to redraw it. Every row is trimmed to the
// terminal's width first, so that count is also the number of lines the
// terminal displays — see asker.width.
func (a *asker) paintList(question string, opts []choice, on []bool, cur int) int {
	q, h := fitPair(question, listHint, a.budget(stepPrefix))
	a.step(a.st.accent(symActive), q, h)

	width := 0
	for _, o := range opts {
		if n := len([]rune(o.label)); n > width {
			width = n
		}
	}

	budget := a.budget(rowPrefix)
	for i, o := range opts {
		mark, here := markOff, "  "
		if on[i] {
			mark = markOn
		}
		if i == cur {
			here = a.st.accent(markHere) + " "
		}

		// Only a row with something to its right needs padding, and it is
		// padded before it is trimmed and coloured: the escape bytes count
		// towards a %-*s width and would leave the hint column ragged.
		label := o.label
		if o.hint != "" {
			label = fmt.Sprintf("%-*s", width, o.label)
		}
		label, hint := fitPair(label, o.hint, budget)

		if on[i] {
			label = a.st.success(label)
		} else if i == cur {
			label = a.st.strong(label)
		}
		a.printf("%s  %s%s %s", a.st.muted(symBar), here, mark, label)
		if hint != "" {
			a.printf("  %s", a.st.muted(hint))
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
		i := slices.Index(keys, part)
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

// toggle flips one row and leaves the selection legal. Rather than refusing
// the key or greying rows out — which needs a disabled state, an explanation
// of why space does nothing, and a way back out — an exclusive row simply
// clears the others as it goes on, and is cleared by them. You watch it
// happen, and every selection stays one key press away.
func toggle(opts []choice, on []bool, i int) {
	on[i] = !on[i]
	if !on[i] {
		return
	}
	for j := range on {
		if j != i && (opts[i].exclusive || opts[j].exclusive) {
			on[j] = false
		}
	}
}

// selectAll turns on everything that can be held at once, or clears the list
// if that is already the case. An exclusive row is never part of "everything":
// including it would produce the one answer the list exists to prevent.
func selectAll(opts []choice, on []bool) {
	full := true
	for i, o := range opts {
		if !o.exclusive && !on[i] {
			full = false
			break
		}
	}
	for i, o := range opts {
		on[i] = !full && !o.exclusive
	}
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
