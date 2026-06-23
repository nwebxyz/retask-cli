package sandbox

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// ansiRE matches CSI escape sequences (colors, cursor moves, screen clears) and
// OSC sequences (e.g. window-title sets) so they can be stripped before
// character-class normalization — otherwise their trailing letters/digits
// (e.g. the "1m" in "\x1b[1m") would survive as spurious text. The rendered
// screen from the emulator is already plain text, but stripping is cheap and
// keeps normalize robust to any residual sequences.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07]*\x07")

// rule maps a normalized substring of an agent's interactive prompt to the
// keystrokes that accept it. See defaultPromptRules for the shipped set.
type rule struct {
	name  string // log identifier
	match string // normalized (see normalize) substring that detects the prompt
	send  string // bytes written to the PTY stdin when matched
}

// defaultPromptRules returns the prompt-acceptance rules applied to a session's
// rendered terminal screen. Extend by appending a rule. Currently handles only
// Claude Code's startup folder-trust dialog.
func defaultPromptRules() []rule {
	return []rule{
		// Claude Code startup trust dialog. Anchor on the affirmative menu option
		// ("...I trust this folder"), NOT the headline or descriptive copy: the
		// option label is stable across releases, and matching it means the
		// interactive menu is on screen. Accept the highlighted default with Enter.
		{name: "claude-trust", match: "trust this folder", send: "\r"},
	}
}

// defaultPollInterval is how often the watcher samples the rendered screen.
// Matches the cadence the project shipped with for prompt handling.
const defaultPollInterval = 500 * time.Millisecond

// defaultPromptWindow bounds how long the watcher scans after a session starts.
// Startup prompts appear within seconds; stopping afterward avoids a late
// false-positive match on ordinary agent output that happens to contain a
// rule's text, and frees the goroutine for sessions whose dialog never shows
// (e.g. an already-trusted folder).
const defaultPromptWindow = 120 * time.Second

// promptWatcher polls a session's rendered terminal screen for known startup
// prompts and injects the accept keystroke once per rule (fire-once). It reads
// the emulator screen rather than the raw byte stream, because TUI agents (e.g.
// Claude Code, built on Ink) draw with cursor positioning and in-place redraws,
// so the prompt text never appears as a contiguous run in the raw output.
type promptWatcher struct {
	screen   func() []string // rendered screen provider (e.g. Runner.Lines)
	stdin    io.Writer       // PTY stdin, where accept keystrokes are injected
	rules    []rule
	interval time.Duration
	window   time.Duration
	log      *slog.Logger // optional
}

func newPromptWatcher(screen func() []string, stdin io.Writer, rules []rule, interval, window time.Duration, log *slog.Logger) *promptWatcher {
	return &promptWatcher{screen: screen, stdin: stdin, rules: rules, interval: interval, window: window, log: log}
}

// Run polls the rendered screen until every rule has fired, the watch window
// elapses, or ctx is cancelled. It owns w.rules for its lifetime, so no locking
// is needed. Intended to run in its own goroutine.
func (w *promptWatcher) Run(ctx context.Context) {
	if len(w.rules) == 0 {
		return
	}
	deadline := time.Now().Add(w.window)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !time.Now().Before(deadline) {
				return
			}
			norm := normalize([]byte(strings.Join(w.screen(), "\n")))
			kept := w.rules[:0]
			for _, r := range w.rules {
				if strings.Contains(norm, r.match) {
					w.fire(r)
					continue // fire-once: drop the rule
				}
				kept = append(kept, r)
			}
			w.rules = kept
			if len(w.rules) == 0 {
				return
			}
		}
	}
}

// fire injects a matched rule's accept keystroke and logs the outcome.
func (w *promptWatcher) fire(r rule) {
	if w.log != nil {
		w.log.Info("prompt_detected", "rule", r.name, "match", r.match)
	}
	if _, err := w.stdin.Write([]byte(r.send)); err != nil {
		if w.log != nil {
			w.log.Error("prompt_autorespond_failed", "rule", r.name, "error", err)
		}
		return
	}
	if w.log != nil {
		w.log.Info("prompt_autoresponded", "rule", r.name)
	}
}

// normalize reduces terminal text to lowercase ASCII words separated by single
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
