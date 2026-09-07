package main

import (
	"os"
	"strconv"
	"strings"
)

// ANSI escape sequences. A terminal acts on them; anything else — a file, a
// pipe, a CI log — prints them as literal noise, which is what `enabled`
// exists to prevent.
const (
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiCyan  = "\033[36m"
	ansiGreen = "\033[32m"
	ansiWarn  = "\033[1;33m"
	ansiRed   = "\033[1;31m"
	ansiReset = "\033[0m"
)

// style decides whether output is coloured and applies it. The zero value is
// plain, so tests and non-terminal runs need do nothing.
type style struct{ enabled bool }

// newStyle reads stdout, not stdin: `seed > plan.txt` from a terminal should
// still ask its questions, and still not colour the file. NO_COLOR is the
// convention for turning this off everywhere, regardless.
func newStyle() style {
	return style{enabled: isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""}
}

func (s style) wrap(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	// Re-open the colour after any nested reset. Without this a styled word
	// inside a styled line — "production" inside the confirmation — ends the
	// outer colour too, and the rest of the line comes out plain.
	if strings.Contains(text, ansiReset) {
		text = strings.ReplaceAll(text, ansiReset, ansiReset+code)
	}
	return code + text + ansiReset
}

// The question itself, so it stands out from the answer typed after it.
func (s style) question(text string) string { return s.wrap(ansiBold, text) }

// Defaults and hints — present, but not competing with the question.
func (s style) hint(text string) string { return s.wrap(ansiDim, text) }

func (s style) duty(text string) string { return s.wrap(ansiCyan, text) }
func (s style) room(text string) string { return s.wrap(ansiGreen, text) }

// warning is for the one line where a wrong answer costs something.
func (s style) warning(text string) string { return s.wrap(ansiWarn, text) }
func (s style) danger(text string) string  { return s.wrap(ansiRed, text) }

// The frame, borrowed from the shape @clack/prompts made familiar (and
// Laravel Prompts after it): every line of a run hangs off one vertical rail,
// so the questions, the plan and the confirmation read as a single block
// instead of as unrelated printf output.
const (
	symIntro  = "┌"
	symBar    = "│"
	symBranch = "├"
	symOutro  = "└"
	symActive = "◆" // the question being answered
	symDone   = "◇" // one already answered
	symWarn   = "▲" // the one answer that costs something
	symStop   = "■" // cancelled
	markOn    = "◼"
	markOff   = "◻"
	markHere  = "▸"
)

// Cursor motion, used to collapse a finished prompt into its answer. Only ever
// written when stdout is a terminal — see asker.redraw.
const (
	ansiClearDown = "\033[0J"
)

func ansiUp(n int) string {
	if n <= 0 {
		return ""
	}
	return "\033[" + strconv.Itoa(n) + "A"
}

// The rail and the hints beside it: present, but never competing with the
// question or the answer.
func (s style) rail(text string) string { return s.hint(text) }

// active is the question on screen right now, done is one already answered.
func (s style) active(text string) string { return s.wrap(ansiCyan, text) }
func (s style) done(text string) string   { return s.wrap(ansiGreen, text) }

// value is an answer echoed back.
func (s style) value(text string) string { return s.wrap(ansiCyan, text) }

// current is the row the cursor is on.
func (s style) current(text string) string { return s.wrap(ansiBold, text) }
