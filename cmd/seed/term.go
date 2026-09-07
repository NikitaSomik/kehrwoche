package main

import (
	"bufio"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// errCancelled is what a prompt returns when the answer was Ctrl+C or Esc
// rather than a choice. It travels up to run, which reports the cancellation
// and leaves the database alone.
var errCancelled = errors.New("cancelled")

// A key press, decoded far enough for a list to react to it. Anything the
// list has no use for arrives as keyOther and is ignored, so an unrecognised
// escape sequence can't be mistaken for a space or an enter.
type keyKind int

const (
	keyOther keyKind = iota
	keyRune
	keyUp
	keyDown
	keyEnter
	keySpace
	keyInterrupt
)

type key struct {
	kind keyKind
	r    rune
}

// readKey decodes one key press. Arrow keys arrive as an escape sequence
// (ESC [ A), so a lone ESC can only be told apart from the start of one by
// whether more bytes are already waiting — a terminal delivers a sequence in
// a single read, a person pressing Esc delivers nothing after it.
func readKey(in *bufio.Reader) (key, error) {
	b, err := in.ReadByte()
	if err != nil {
		return key{}, err
	}

	switch b {
	case 3: // Ctrl+C. In raw mode this is a byte, not a signal.
		return key{kind: keyInterrupt}, nil
	case '\r', '\n':
		return key{kind: keyEnter}, nil
	case ' ':
		return key{kind: keySpace}, nil
	case 'k':
		return key{kind: keyUp}, nil
	case 'j':
		return key{kind: keyDown}, nil
	case 27: // ESC
		if in.Buffered() == 0 {
			return key{kind: keyInterrupt}, nil
		}
		if c, err := in.ReadByte(); err != nil || c != '[' {
			return key{kind: keyOther}, nil
		}
		c, err := in.ReadByte()
		if err != nil {
			return key{}, err
		}
		switch c {
		case 'A':
			return key{kind: keyUp}, nil
		case 'B':
			return key{kind: keyDown}, nil
		}
		return key{kind: keyOther}, nil
	}
	return key{kind: keyRune, r: rune(b)}, nil
}

// isTerminal reports whether f is a terminal — the check that decides whether
// anything is asked at all.
//
// It asks the terminal driver rather than reading the file mode. A mode check
// looks for a character device, and /dev/null is one: `seed < /dev/null` would
// be taken for somebody sitting at a keyboard, and a script would be prompted
// into rather than run on its defaults.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// terminalWidth is how many columns f has, or 0 when that can't be known —
// f isn't a terminal, or the ioctl failed. Callers treat 0 as "don't trim".
func terminalWidth(f *os.File) int {
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// rawMode puts the terminal into raw mode and returns the function that undoes
// it. Raw mode is what makes a key press readable the moment it happens, and
// it is also what makes a crashed program leave a shell that echoes nothing —
// so every path out of it restores: the deferred call, a panic unwinding
// through it, or a signal.
func rawMode(f *os.File) (restore func(), err error) {
	fd := int(f.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}

	var once sync.Once
	undo := func() { once.Do(func() { _ = term.Restore(fd, old) }) }

	// Ctrl+C arrives as a byte in raw mode rather than a signal, so this is
	// here for the ones that don't: a kill, a closed terminal window.
	// Restoring and then re-raising leaves the default behaviour intact, only
	// not before the terminal is usable again.
	sig := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case s := <-sig:
			undo()
			signal.Stop(sig)
			if ss, ok := s.(syscall.Signal); ok {
				_ = syscall.Kill(os.Getpid(), ss)
			}
		case <-done:
		}
	}()

	var stopped sync.Once
	return func() {
		stopped.Do(func() {
			signal.Stop(sig)
			close(done)
		})
		undo()
	}, nil
}
