package sandbox

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// ansiRE matches CSI escape sequences (colors, cursor moves, screen clears) and
// OSC sequences (e.g. window-title sets) so they can be stripped before
// character-class normalization — otherwise their trailing letters/digits
// (e.g. the "1m" in "\x1b[1m") would survive as spurious text.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07]*\x07")

// rule maps a normalized substring of an agent's interactive prompt to the
// keystrokes that accept it. See defaultPromptRules for the shipped set.
type rule struct {
	name  string // log identifier
	match string // normalized (see normalize) substring that detects the prompt
	send  string // bytes written to the PTY stdin when matched
}

// defaultPromptRules returns the prompt-acceptance rules applied to a session's
// PTY output. Extend by appending a rule. Currently handles only Claude Code's
// startup folder-trust dialog.
func defaultPromptRules() []rule {
	return []rule{
		// Claude Code trust dialog: "...read, edit, and execute files here."
		// Accept the highlighted default option ("Yes, proceed") with Enter.
		{name: "claude-trust", match: "execute files here", send: "\r"},
	}
}

// promptBufferCap bounds the rolling window kept over recent PTY output.
const promptBufferCap = 8 << 10 // 8 KiB

// promptResponder wraps a PTY output writer. It passes all output through
// unaltered while scanning a rolling, normalized window for known prompts; when
// one matches it injects the accept keystrokes into the PTY stdin and drops the
// rule (fire-once). Once no rules remain it degrades to a transparent
// pass-through with no scanning overhead.
type promptResponder struct {
	inner io.Writer    // real output sink (e.g. wsWriter)
	stdin io.Writer    // PTY stdin, where accept keystrokes are injected
	log   *slog.Logger // optional

	mu    sync.Mutex
	rules []rule
	buf   []byte
}

func newPromptResponder(inner, stdin io.Writer, rules []rule, log *slog.Logger) *promptResponder {
	return &promptResponder{inner: inner, stdin: stdin, log: log, rules: rules}
}

// Write passes p through to the inner writer unaltered, then (while rules
// remain) scans the rolling buffer and injects accept keystrokes for any rule
// that matches. The pass-through happens outside the lock so a blocking sink
// (the WebSocket) never holds up rule state.
func (pr *promptResponder) Write(p []byte) (n int, err error) {
	n, err = pr.inner.Write(p)
	if err != nil {
		return n, err
	}

	pr.mu.Lock()
	defer pr.mu.Unlock()
	if len(pr.rules) == 0 {
		return n, err // quick bypass: nothing left to match
	}

	pr.buf = append(pr.buf, p...)
	if len(pr.buf) > promptBufferCap {
		pr.buf = pr.buf[len(pr.buf)-promptBufferCap:]
	}
	norm := normalize(pr.buf)

	kept := pr.rules[:0]
	for _, r := range pr.rules {
		if strings.Contains(norm, r.match) {
			pr.inject(r)
			continue // auto-deregister: drop the fired rule
		}
		kept = append(kept, r)
	}
	pr.rules = kept
	if len(pr.rules) == 0 {
		pr.buf = nil // release the window once there is nothing left to scan
	}
	return n, err
}

func (pr *promptResponder) inject(r rule) {
	if _, err := pr.stdin.Write([]byte(r.send)); err != nil {
		if pr.log != nil {
			pr.log.Error("prompt_autorespond_failed", "rule", r.name, "error", err)
		}
		return
	}
	if pr.log != nil {
		pr.log.Info("prompt_autoresponded", "rule", r.name)
	}
}

// normalize reduces terminal output to lowercase ASCII words separated by single
// spaces so prompt text matches despite ANSI escapes, box-drawing characters,
// and word wrapping. Every rune outside [a-z0-9 ] (after lowercasing) becomes a
// space; whitespace runs collapse to one space; leading/trailing spaces are
// trimmed.
func normalize(b []byte) string {
	b = ansiRE.ReplaceAll(b, nil)
	var sb strings.Builder
	sb.Grow(len(b))
	space := true // suppresses leading and repeated spaces
	for _, r := range string(b) {
		switch {
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r + ('a' - 'A'))
			space = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			space = false
		default:
			if !space {
				sb.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimRight(sb.String(), " ")
}
