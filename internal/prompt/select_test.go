package prompt

import (
	"bufio"
	"bytes"
	"strings"
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

func TestRenderListIncludesNameAndDetail(t *testing.T) {
	var buf bytes.Buffer
	items := []Item{
		{ID: "ws_1", Name: "Engineering", Detail: "ws_1"},
		{ID: "ws_2", Name: "Design", Detail: "ws_2"},
	}
	renderList(&buf, items, 0)
	out := buf.String()

	for _, want := range []string{"Engineering", "ws_1", "Design", "ws_2"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderList output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderListOmitsParensForEmptyDetail(t *testing.T) {
	var buf bytes.Buffer
	renderList(&buf, []Item{{ID: "x", Name: "Solo"}}, 0)
	if strings.Contains(buf.String(), "()") {
		t.Errorf("renderList should omit empty parens, got:\n%s", buf.String())
	}
}
