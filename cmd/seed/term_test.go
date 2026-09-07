package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadKey(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  keyKind
	}{
		{"up arrow", "\x1b[A", keyUp},
		{"down arrow", "\x1b[B", keyDown},
		{"k moves up", "k", keyUp},
		{"j moves down", "j", keyDown},
		{"enter", "\r", keyEnter},
		{"newline is enter too", "\n", keyEnter},
		{"space", " ", keySpace},
		{"ctrl-c", "\x03", keyInterrupt},
		{"a lone escape cancels", "\x1b", keyInterrupt},
		{"an unknown sequence is ignored", "\x1b[Z", keyOther},
		{"anything else is a rune", "a", keyRune},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, err := readKey(bufio.NewReader(strings.NewReader(tc.input)))
			if err != nil {
				t.Fatalf("readKey: %v", err)
			}
			if k.kind != tc.want {
				t.Errorf("got kind %v, want %v", k.kind, tc.want)
			}
		})
	}
}

// The arrow keys and a lone Esc both start with the same byte. They can only be
// told apart by whether the rest of the sequence has already arrived, so a
// press of Esc must not swallow the key typed after it.
func TestReadKeyEscapeDoesNotEatTheNextKey(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("\x1b"))
	if k, _ := readKey(in); k.kind != keyInterrupt {
		t.Fatalf("a lone escape read as %v", k.kind)
	}
}

func TestReadKeyReportsAClosedInput(t *testing.T) {
	if _, err := readKey(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Error("got nil, want an error for input that ended")
	}
}
