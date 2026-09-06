package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// asker fills in what the caller left off the command line.
//
// Flags always win: only a value that wasn't given is asked for, which is how
// -vacant has always behaved. And it is only asked when there is somebody to
// answer — piping or redirecting stdin turns every prompt back into its
// default, so a script keeps behaving like a script.
type asker struct {
	in          *bufio.Reader
	out         io.Writer
	interactive bool
	st          style
}

// newAsker reads the terminal, if this is one. Deciding once here means the
// prompts don't each have to wonder.
func newAsker() *asker {
	return &asker{
		in:          bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		interactive: isTerminal(os.Stdin),
		st:          newStyle(),
	}
}

// isTerminal reports whether f is a character device — a terminal — rather
// than a pipe or a file. os.Stat is enough for this; no dependency needed.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// line asks for a string, returning def when the answer is empty. def is also
// what comes back when nobody is there to ask.
func (a *asker) line(question, def string) string {
	if !a.interactive {
		return def
	}
	if def != "" {
		fmt.Fprintf(a.out, "%s %s: ", a.st.question(question), a.st.hint("["+def+"]"))
	} else {
		fmt.Fprintf(a.out, "%s: ", a.st.question(question))
	}
	answer, err := a.in.ReadString('\n')
	if err != nil && answer == "" {
		// stdin closed mid-session; treat it as accepting the default rather
		// than looping on an input that will never arrive.
		return def
	}
	if answer = strings.TrimSpace(answer); answer == "" {
		return def
	}
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
		fmt.Fprintf(a.out, "  %s\n", a.st.warning(fmt.Sprintf("%q is not a number", answer)))
	}
}

// confirm asks a yes/no question. Anything but an explicit yes is a no, so a
// stray newline can't approve a write to the production database.
func (a *asker) confirm(question string) bool {
	if !a.interactive {
		// Non-interactive runs are scripts that already said what they wanted
		// on the command line; blocking them on a prompt nobody sees would
		// hang instead of protecting anything.
		return true
	}
	fmt.Fprintf(a.out, "%s %s: ", a.st.warning(question), a.st.hint("[y/N]"))
	answer, _ := a.in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
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
