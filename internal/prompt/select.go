// internal/prompt/select.go
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Item is a single selectable entry in an interactive list.
type Item struct {
	ID    string
	Label string
}

// Key is a logical key press recognized by the picker.
type Key int

const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyCancel
)

// IsInteractive reports whether stdin is attached to a terminal.
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// readKey reads and resolves a single logical key press from r, consuming
// multi-byte ANSI escape sequences for the arrow keys.
func readKey(r *bufio.Reader) (key Key, err error) {
	b, err := r.ReadByte()
	if err != nil {
		return KeyNone, err
	}
	switch b {
	case 0x03, 'q':
		return KeyCancel, nil
	case '\r', '\n':
		return KeyEnter, nil
	case 'k':
		return KeyUp, nil
	case 'j':
		return KeyDown, nil
	case 0x1b:
		b2, err := r.ReadByte()
		if err != nil || b2 != '[' {
			return KeyNone, nil
		}
		b3, err := r.ReadByte()
		if err != nil {
			return KeyNone, nil
		}
		switch b3 {
		case 'A':
			return KeyUp, nil
		case 'B':
			return KeyDown, nil
		}
		return KeyNone, nil
	default:
		return KeyNone, nil
	}
}

// nextCursor applies key to cursor, clamped to [0, length-1].
func nextCursor(cursor, length int, key Key) int {
	switch key {
	case KeyUp:
		if cursor > 0 {
			cursor--
		}
	case KeyDown:
		if cursor < length-1 {
			cursor++
		}
	}
	return cursor
}

// SelectOne renders items on out and lets the user move with the up/down
// arrow keys (or j/k), choosing with Enter. Ctrl+C or 'q' cancels. Callers
// must check IsInteractive() first — SelectOne puts stdin into raw mode and
// requires a real terminal.
func SelectOne(out io.Writer, items []Item) (id string, err error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no items to select from")
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("enable raw terminal mode: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	cursor := 0
	renderList(out, items, cursor)

	reader := bufio.NewReader(os.Stdin)
	for {
		key, err := readKey(reader)
		if err != nil {
			return "", err
		}
		switch key {
		case KeyCancel:
			return "", fmt.Errorf("selection cancelled")
		case KeyEnter:
			return items[cursor].ID, nil
		case KeyUp, KeyDown:
			cursor = nextCursor(cursor, len(items), key)
			fmt.Fprintf(out, "\x1b[%dA", len(items))
			renderList(out, items, cursor)
		}
	}
}

func renderList(out io.Writer, items []Item, cursor int) {
	for i, it := range items {
		prefix := "  "
		if i == cursor {
			prefix = "> "
		}
		fmt.Fprintf(out, "\x1b[2K\r%s%s\r\n", prefix, it.Label)
	}
}
