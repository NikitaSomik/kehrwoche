package main

import (
	"bufio"
	"strings"
	"testing"
	"testing/iotest"
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
		{"a lone escape means nothing", "\x1b", keyOther},
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

// An escape sequence usually arrives in one read, but over ssh or a busy pty
// it can arrive a byte at a time — and then it is indistinguishable from Esc
// followed by two ordinary keys. Nothing it decays into may do anything: an
// arrow key that costs a press nobody notices is the good outcome, where a
// cancelled run or a select-all is not.
func TestReadKeySplitEscapeSequenceIsHarmless(t *testing.T) {
	in := bufio.NewReader(iotest.OneByteReader(strings.NewReader("\x1b[A")))

	for i, want := range []struct {
		kind keyKind
		r    rune
	}{{keyOther, 0}, {keyRune, '['}, {keyRune, 'A'}} {
		k, err := readKey(in)
		if err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		if k.kind == keyInterrupt {
			t.Fatalf("byte %d of a split arrow key cancelled the run", i)
		}
		if k.kind != want.kind || (want.r != 0 && k.r != want.r) {
			t.Errorf("byte %d: got %v/%q, want %v/%q", i, k.kind, k.r, want.kind, want.r)
		}
	}
}

// The tail of such a sequence reaches navigate as 'A'. Select-all answers to
// lower case only, so it does nothing.
func TestNavigateIgnoresUppercaseA(t *testing.T) {
	opts, on := fourRooms()
	a, _ := newListAsker("A\r")

	got, err := a.navigate("Vacant rooms", opts, on)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if summarise(opts, got) != "" {
		t.Errorf("got %q, want nothing selected", summarise(opts, got))
	}
}

func TestReadKeyReportsAClosedInput(t *testing.T) {
	if _, err := readKey(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Error("got nil, want an error for input that ended")
	}
}
