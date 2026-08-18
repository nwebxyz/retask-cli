package prompt

import (
	"bufio"
	"bytes"
	"testing"
)

func TestReadKeyArrowUp(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte("\x1b[A")))
	key, err := readKey(r)
	if err != nil {
		t.Fatal(err)
	}
	if key != KeyUp {
		t.Errorf("readKey = %v, want KeyUp", key)
	}
}

func TestReadKeyArrowDown(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte("\x1b[B")))
	key, err := readKey(r)
	if err != nil {
		t.Fatal(err)
	}
	if key != KeyDown {
		t.Errorf("readKey = %v, want KeyDown", key)
	}
}

func TestReadKeyVimBindings(t *testing.T) {
	cases := map[byte]Key{'j': KeyDown, 'k': KeyUp}
	for b, want := range cases {
		r := bufio.NewReader(bytes.NewReader([]byte{b}))
		got, err := readKey(r)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("readKey(%q) = %v, want %v", b, got, want)
		}
	}
}

func TestReadKeyEnter(t *testing.T) {
	for _, b := range []byte{'\r', '\n'} {
		r := bufio.NewReader(bytes.NewReader([]byte{b}))
		got, err := readKey(r)
		if err != nil {
			t.Fatal(err)
		}
		if got != KeyEnter {
			t.Errorf("readKey(%q) = %v, want KeyEnter", b, got)
		}
	}
}

func TestReadKeyCancel(t *testing.T) {
	for _, b := range []byte{0x03, 'q'} {
		r := bufio.NewReader(bytes.NewReader([]byte{b}))
		got, err := readKey(r)
		if err != nil {
			t.Fatal(err)
		}
		if got != KeyCancel {
			t.Errorf("readKey(%q) = %v, want KeyCancel", b, got)
		}
	}
}

func TestReadKeyUnknownEscapeIsIgnored(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte("\x1b[Z")))
	got, err := readKey(r)
	if err != nil {
		t.Fatal(err)
	}
	if got != KeyNone {
		t.Errorf("readKey = %v, want KeyNone", got)
	}
}

func TestReadKeyEOF(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader(nil))
	if _, err := readKey(r); err == nil {
		t.Fatal("expected EOF error")
	}
}

func TestNextCursorClampsAtBounds(t *testing.T) {
	if got := nextCursor(0, 3, KeyUp); got != 0 {
		t.Errorf("nextCursor(0, 3, KeyUp) = %d, want 0", got)
	}
	if got := nextCursor(2, 3, KeyDown); got != 2 {
		t.Errorf("nextCursor(2, 3, KeyDown) = %d, want 2", got)
	}
}

func TestNextCursorMoves(t *testing.T) {
	if got := nextCursor(1, 3, KeyUp); got != 0 {
		t.Errorf("nextCursor(1, 3, KeyUp) = %d, want 0", got)
	}
	if got := nextCursor(1, 3, KeyDown); got != 2 {
		t.Errorf("nextCursor(1, 3, KeyDown) = %d, want 2", got)
	}
}

func TestSelectOneEmptyItems(t *testing.T) {
	if _, err := SelectOne(new(bytes.Buffer), nil); err == nil {
		t.Fatal("expected error for empty items")
	}
}
