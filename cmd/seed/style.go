package main

import "os"

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
