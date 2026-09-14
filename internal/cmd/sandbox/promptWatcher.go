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

// Keystrokes written to the PTY to drive an agent's arrow-key menu.
const (
	keyUp      = "\x1b[A"
	keyDown    = "\x1b[B"
	keyConfirm = "\r"
)

// focusMarkers are the leading glyphs an Ink-style select list uses to mark the
// focused row. Deliberately narrow — no bare ">" — because a false marker means
// confirming the wrong option, whereas failing to find one only means the
// watcher waits and then leaves the prompt alone.
var focusMarkers = []string{"❯", "›", "▶", "▸"}

// maxFocusSteps bounds how many arrow keys a rule may send while moving focus
// onto its accepting option. A menu whose focus moves without ever reaching the
// option (e.g. one whose option is off screen) exhausts the budget and the rule
// is abandoned untouched.
const maxFocusSteps = 12

// focusSettlePolls is how many polls the watcher waits for an arrow key to show
// up on screen as moved focus (3s at defaultPollInterval) before abandoning the
// rule. No other key is sent while one is unconfirmed: keys queued by a busy
// agent would be applied after the watcher read a stale focus and could put
// Enter on the wrong option, and a screen that does not reflect keys at all
// (e.g. an emulator that lost track of the cursor) is not one to act on.
const focusSettlePolls = 6

// rule describes an interactive agent prompt and which option accepts it.
// See defaultPromptRules for the shipped set.
type rule struct {
	name   string // log identifier
	match  string // normalized (see normalize) substring that detects the prompt
	accept string // normalized substring of the option line that must hold focus
}

// defaultPromptRules returns the prompt-acceptance rules applied to a session's
// rendered terminal screen. Extend by appending a rule. Handles the startup
// folder/workspace-trust dialogs shipped by Claude Code and Codex.
func defaultPromptRules() []rule {
	return []rule{
		// Claude Code startup trust dialog. Anchor on the affirmative menu option
		// ("...I trust this folder"), NOT the headline or descriptive copy: the
		// option label is stable across releases, and matching it means the
		// interactive menu is on screen. The same text identifies the row that
		// must hold focus before Enter is pressed — Claude Code renders this
		// dialog cancel-first and focuses the cancel option ("No, exit"), so a
		// bare Enter would decline and exit the agent.
		{name: "claude-trust", match: "trust this folder", accept: "trust this folder"},
		// Codex startup workspace-trust dialog. Anchor detection on the dialog's
		// descriptive copy ("...trust the contents of this directory...") rather
		// than the numbered option labels, whose leading digits are common enough
		// in ordinary agent output to false-positive on. accept is deliberately
		// just "yes", not the full "yes continue" label: the dialog's own body
		// copy never contains "yes", so the shorter anchor still lands on the
		// right row while surviving a label reword that drops or changes
		// "continue" (unlike claude-trust's accept, which can't be shortened the
		// same way — Claude Code's own headline already contains "trust", so
		// trimming its accept to "trust" would match that headline instead of the
		// option row). Codex ships this dialog affirmative-first, focused on
		// "1. Yes, continue" by default — the opposite layout from Claude Code's
		// cancel-first dialog — but the watcher still steps focus onto the
		// accepting option itself rather than trusting that default: option order
		// is agent- and release-specific, and it has already changed once for
		// Claude Code.
		{name: "codex-trust", match: "trust the contents of this directory", accept: "yes"},
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
// prompts and accepts each one once (fire-once). It reads the emulator screen
// rather than the raw byte stream, because TUI agents (e.g. Claude Code, built
// on Ink) draw with cursor positioning and in-place redraws, so the prompt text
// never appears as a contiguous run in the raw output.
//
// Acceptance is focus-aware: the watcher locates the focus marker and the
// accepting option on the rendered screen and steps focus onto that option
// before pressing Enter, rather than trusting the menu's default. Agents place
// the default on the DECLINING option for consequential prompts, so a bare
// Enter would exit the agent — and the ordering has already changed once.
type promptWatcher struct {
	screen   func() []string // rendered screen provider (e.g. Runner.Lines)
	stdin    io.Writer       // PTY stdin, where keystrokes are injected
	rules    []rule
	interval time.Duration
	window   time.Duration
	log      *slog.Logger       // optional
	steps    map[string]int     // remaining focus-navigation budget, by rule name
	sent     map[string]sentKey // arrow key not yet reflected on screen, by rule name
	detected map[string]bool    // rules already logged as detected, by rule name
	answered map[string]bool    // rules already resolved or reported, by rule name
}

// sentKey records an arrow key awaiting its effect: the focused row when it was
// sent, and how many polls have shown focus still on that row.
type sentKey struct {
	focus int
	polls int
}

func newPromptWatcher(screen func() []string, stdin io.Writer, rules []rule, interval, window time.Duration, log *slog.Logger) *promptWatcher {
	steps := make(map[string]int, len(rules))
	for _, r := range rules {
		steps[r.name] = maxFocusSteps
	}
	return &promptWatcher{
		screen:   screen,
		stdin:    stdin,
		rules:    rules,
		interval: interval,
		window:   window,
		log:      log,
		steps:    steps,
		sent:     make(map[string]sentKey, len(rules)),
		detected: make(map[string]bool, len(rules)),
		answered: make(map[string]bool, len(rules)),
	}
}

// Run polls the rendered screen until every rule has finished, the watch window
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
				w.leaveRemainingForHuman("window_elapsed")
				return
			}
			lines := w.screen()
			norm := normalize([]byte(strings.Join(lines, "\n")))
			kept := w.rules[:0]
			for _, r := range w.rules {
				// A rule survives the tick unless it is finished: accepted, or
				// abandoned. Each tick advances an on-screen prompt by one step.
				if strings.Contains(norm, r.match) && w.advance(r, lines) {
					continue
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

// advance takes one step on a detected prompt: it presses Enter when the
// accepting option already holds focus, otherwise moves focus one row toward it
// and waits for the screen to show that move before sending anything else.
// It reports whether the rule is finished — accepted, abandoned after
// maxFocusSteps or focusSettlePolls, or dropped because stdin failed.
//
// Abandoning leaves the dialog untouched for a human to answer. That is the safe
// outcome: pressing Enter without knowing what holds focus is what selects
// "No, exit".
func (w *promptWatcher) advance(r rule, lines []string) (done bool) {
	if !w.detected[r.name] {
		w.detected[r.name] = true
		w.logInfo("prompt_detected", "rule", r.name, "match", r.match)
	}

	target := lineContaining(lines, r.accept)
	focus := focusedLine(lines)
	if target < 0 || focus < 0 {
		return false // option list not rendered yet — keep waiting
	}

	if k, ok := w.sent[r.name]; ok {
		if focus == k.focus {
			if k.polls >= focusSettlePolls {
				w.leaveForHuman(r, "focus_not_moving")
				return true
			}
			k.polls++
			w.sent[r.name] = k
			return false // last key not reflected on screen yet — send nothing
		}
		delete(w.sent, r.name)
	}

	if focus == target {
		if !w.press(r, keyConfirm) {
			return true
		}
		w.logInfo("prompt_autoresponded", "rule", r.name)
		return true
	}

	if w.steps[r.name] <= 0 {
		w.leaveForHuman(r, "focus_unreachable")
		return true
	}
	w.steps[r.name]--
	key := keyDown
	if target < focus {
		key = keyUp
	}
	if !w.press(r, key) {
		return true
	}
	w.sent[r.name] = sentKey{focus: focus}
	return false
}

// press writes keys to the PTY stdin, reporting whether the write succeeded.
func (w *promptWatcher) press(r rule, keys string) (ok bool) {
	if _, err := w.stdin.Write([]byte(keys)); err != nil {
		w.logError("prompt_autorespond_failed", "rule", r.name, "error", err)
		return false
	}
	return true
}

// leaveForHuman records that a detected prompt was deliberately NOT answered.
// The watcher never closes the PTY or ends the session, so the dialog stays on
// screen and stays interactive: an operator can attach and answer it by hand.
func (w *promptWatcher) leaveForHuman(r rule, reason string) {
	w.answered[r.name] = true // report once per rule
	w.logWarn("prompt_left_for_human",
		"rule", r.name, "accept", r.accept, "reason", reason,
		"detail", "prompt not auto-accepted; session left running for a manual answer")
}

// leaveRemainingForHuman reports every prompt that was detected but never
// answered — e.g. an agent release that renamed the option we accept on.
func (w *promptWatcher) leaveRemainingForHuman(reason string) {
	for _, r := range w.rules {
		if w.detected[r.name] && !w.answered[r.name] {
			w.leaveForHuman(r, reason)
		}
	}
}

func (w *promptWatcher) logInfo(msg string, args ...any) {
	if w.log != nil {
		w.log.Info(msg, args...)
	}
}

func (w *promptWatcher) logWarn(msg string, args ...any) {
	if w.log != nil {
		w.log.Warn(msg, args...)
	}
}

func (w *promptWatcher) logError(msg string, args ...any) {
	if w.log != nil {
		w.log.Error(msg, args...)
	}
}

// lineContaining returns the index of the first line whose normalized text
// contains want, or -1. Matching per line (rather than on the whole screen)
// is what lets the watcher compare an option's position against the focus.
func lineContaining(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(normalize([]byte(l)), want) {
			return i
		}
	}
	return -1
}

// focusedLine returns the index of the first line whose leading glyph is a
// focus marker, or -1 when no option is focused (or the menu has not rendered).
func focusedLine(lines []string) int {
	for i, l := range lines {
		trimmed := strings.TrimSpace(string(ansiRE.ReplaceAll([]byte(l), nil)))
		for _, m := range focusMarkers {
			if strings.HasPrefix(trimmed, m) {
				return i
			}
		}
	}
	return -1
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
