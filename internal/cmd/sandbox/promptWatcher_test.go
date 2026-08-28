package sandbox

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- normalize ---

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ansi stripped", "\x1b[1mHello\x1b[0m World", "hello world"},
		{"punctuation to space", "read, edit, and execute files here.", "read edit and execute files here"},
		{"box drawing and wrap", "│ execute │\r\n│ files here │", "execute files here"},
		{"digits kept", "❯ 1. Yes, proceed", "1 yes proceed"},
		{"collapse runs", "a\t\t  b\n\n\nc", "a b c"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, normalize([]byte(tc.in)), "case=%s", tc.name)
	}
}

func TestDefaultPromptRules_MatchesNormalizedTrustOption(t *testing.T) {
	// The rendered trust-option line, as the emulator would surface it. Both the
	// current (unnumbered) and the historical (numbered) forms must match.
	for _, line := range []string{"  Yes, I trust this folder", "❯ 1. Yes, I trust this folder"} {
		norm := normalize([]byte(line))
		rules := defaultPromptRules()
		assert.NotEmpty(t, rules)
		for _, r := range rules {
			if r.name == "claude-trust" {
				assert.True(t, strings.Contains(norm, r.match),
					"claude-trust match %q not found in %q", r.match, norm)
				assert.True(t, strings.Contains(norm, r.accept),
					"claude-trust accept %q not found in %q", r.accept, norm)
			}
		}
	}
}

// --- test doubles ---

// lockedBuffer is a goroutine-safe io.Writer for asserting on output the watcher
// goroutine produces.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fakeSelect emulates the agent's arrow-key select list: it renders the focused
// option with the "❯" marker, moves focus on arrow keys, and records the label
// confirmed with Enter. Focus CLAMPS at the ends (it does not wrap), which is
// the conservative assumption — a watcher that relies on wrap-around would hang
// here rather than silently pick the wrong option.
type fakeSelect struct {
	mu        sync.Mutex
	header    []string
	options   []string
	cursor    int
	confirmed string
	keys      []string
}

func newTrustSelect(options []string, cursor int) *fakeSelect {
	return &fakeSelect{
		header: []string{
			"Quick safety check: Is this a project you created or one you trust?",
			"Claude Code'll be able to read, edit, and execute files here.",
			"",
		},
		options: options,
		cursor:  cursor,
	}
}

// currentTrustSelect is the dialog Claude Code ships today: cancel first, and
// focus starting on cancel.
func currentTrustSelect() *fakeSelect {
	return newTrustSelect([]string{"No, exit", "Yes, I trust this folder"}, 0)
}

func (s *fakeSelect) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(p)
	s.keys = append(s.keys, k)
	switch k {
	case "\x1b[B":
		if s.cursor < len(s.options)-1 {
			s.cursor++
		}
	case "\x1b[A":
		if s.cursor > 0 {
			s.cursor--
		}
	case "\r":
		if s.confirmed == "" {
			s.confirmed = s.options[s.cursor]
		}
	}
	return len(p), nil
}

// Screen renders the dialog, or the post-acceptance screen once confirmed, so a
// watcher that keeps polling cannot re-match a dismissed dialog.
func (s *fakeSelect) Screen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.confirmed != "" {
		return []string{"Welcome to Claude Code", "> "}
	}
	out := append([]string{}, s.header...)
	for i, o := range s.options {
		marker := "  "
		if i == s.cursor {
			marker = "❯ "
		}
		out = append(out, marker+o)
	}
	return out
}

func (s *fakeSelect) Confirmed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmed
}

func (s *fakeSelect) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.keys...)
}

// --- promptWatcher: focus-aware acceptance ---

func TestPromptWatcher_MovesFocusToAffirmativeThenConfirms(t *testing.T) {
	sel := currentTrustSelect() // focus starts on "No, exit"
	w := newPromptWatcher(sel.Screen, sel, defaultPromptRules(), 2*time.Millisecond, 2*time.Second, nil)

	w.Run(context.Background())

	assert.Equal(t, "Yes, I trust this folder", sel.Confirmed(),
		"must move focus off the cancel option before pressing Enter")
}

func TestPromptWatcher_MovesFocusUpwardWhenAffirmativeIsAbove(t *testing.T) {
	// Affirmative first, focus parked on the cancel option below it.
	sel := newTrustSelect([]string{"Yes, I trust this folder", "No, exit"}, 1)
	w := newPromptWatcher(sel.Screen, sel, defaultPromptRules(), 2*time.Millisecond, 2*time.Second, nil)

	w.Run(context.Background())

	assert.Equal(t, "Yes, I trust this folder", sel.Confirmed(),
		"navigates upward when the affirmative option is above the focus")
}

func TestPromptWatcher_ConfirmsWithoutMovingWhenAffirmativeFocused(t *testing.T) {
	sel := newTrustSelect([]string{"Yes, I trust this folder", "No, exit"}, 0)
	w := newPromptWatcher(sel.Screen, sel, defaultPromptRules(), 2*time.Millisecond, 2*time.Second, nil)

	w.Run(context.Background())

	assert.Equal(t, "Yes, I trust this folder", sel.Confirmed())
	assert.Equal(t, []string{"\r"}, sel.Keys(), "no arrow keys when already focused")
}

func TestPromptWatcher_NeverConfirmsWhileCancelStaysFocused(t *testing.T) {
	stdin := &lockedBuffer{}
	// A frozen screen: the dialog is up and focus never leaves "No, exit".
	// Confirming here would exit the agent, so the watcher must give up instead.
	frozen := func() []string {
		return []string{
			"Quick safety check: Is this a project you created or one you trust?",
			"❯ No, exit",
			"  Yes, I trust this folder",
		}
	}
	w := newPromptWatcher(frozen, stdin, defaultPromptRules(), 2*time.Millisecond, 2*time.Second, nil)

	w.Run(context.Background())

	assert.NotContains(t, stdin.String(), "\r",
		"never press Enter while the cancel option holds focus")
}

func TestPromptWatcher_GivesUpAfterBoundedNavigation(t *testing.T) {
	stdin := &lockedBuffer{}
	frozen := func() []string {
		return []string{"❯ No, exit", "  Yes, I trust this folder"}
	}
	// Window far exceeds what bounded navigation needs, so returning early proves
	// the watcher stopped on its own step budget rather than on the deadline.
	w := newPromptWatcher(frozen, stdin, defaultPromptRules(), time.Millisecond, 10*time.Second, nil)

	start := time.Now()
	w.Run(context.Background())

	assert.Less(t, time.Since(start), 5*time.Second, "gave up before the watch window elapsed")
	assert.Equal(t, maxFocusSteps, strings.Count(stdin.String(), "\x1b[B"), "navigation is bounded")
}

func TestPromptWatcher_WaitsWhileNoOptionIsFocusedYet(t *testing.T) {
	stdin := &lockedBuffer{}
	// Dialog text on screen but the option list has not rendered its marker yet.
	partial := func() []string {
		return []string{
			"Quick safety check: Is this a project you created or one you trust?",
			"  Yes, I trust this folder",
		}
	}
	w := newPromptWatcher(partial, stdin, defaultPromptRules(), 2*time.Millisecond, 60*time.Millisecond, nil)

	w.Run(context.Background())

	assert.Empty(t, stdin.String(), "no keystrokes until an option is actually focused")
}

// --- promptWatcher: lifecycle ---

func TestPromptWatcher_FiresOnce(t *testing.T) {
	sel := currentTrustSelect()
	w := newPromptWatcher(sel.Screen, sel, defaultPromptRules(), 2*time.Millisecond, time.Second, nil)

	w.Run(context.Background()) // returns once the single rule fires

	assert.Equal(t, 1, strings.Count(strings.Join(sel.Keys(), ""), "\r"), "confirmed exactly once")
}

func TestPromptWatcher_NoMatchStopsAtWindow(t *testing.T) {
	stdin := &lockedBuffer{}
	screen := func() []string { return []string{"just some normal agent output"} }
	w := newPromptWatcher(screen, stdin, defaultPromptRules(), 5*time.Millisecond, 40*time.Millisecond, nil)

	start := time.Now()
	w.Run(context.Background()) // blocks until the window elapses
	assert.Empty(t, stdin.String(), "no injection when the prompt never appears")
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond, "ran until the watch window")
}

func TestPromptWatcher_StopsOnContextCancel(t *testing.T) {
	stdin := &lockedBuffer{}
	screen := func() []string { return []string{"nothing to match here"} }
	w := newPromptWatcher(screen, stdin, defaultPromptRules(), 5*time.Millisecond, time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop on context cancel")
	}
}

func TestPromptWatcher_EmptyRulesReturnImmediately(t *testing.T) {
	sel := currentTrustSelect()
	w := newPromptWatcher(sel.Screen, sel, nil, 5*time.Millisecond, time.Hour, nil)
	w.Run(context.Background()) // must not block
	assert.Empty(t, sel.Keys())
}

func TestPromptWatcher_LogsDetectionAndResponse(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	sel := currentTrustSelect()
	w := newPromptWatcher(sel.Screen, sel, defaultPromptRules(), 2*time.Millisecond, time.Second, log)

	w.Run(context.Background())

	logs := logBuf.String()
	assert.Contains(t, logs, "prompt_detected", "detection is logged")
	assert.Contains(t, logs, "prompt_autoresponded", "keystroke injection is logged")
	assert.Contains(t, logs, "claude-trust", "the fired rule is named in the logs")
}

func TestPromptWatcher_LogsGiveUpWithoutConfirming(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	stdin := &lockedBuffer{}
	frozen := func() []string {
		return []string{"❯ No, exit", "  Yes, I trust this folder"}
	}
	w := newPromptWatcher(frozen, stdin, defaultPromptRules(), time.Millisecond, 10*time.Second, log)

	w.Run(context.Background())

	assert.Contains(t, logBuf.String(), "prompt_left_for_human",
		"giving up without confirming is logged")
	assert.Contains(t, logBuf.String(), "focus_unreachable", "the reason is recorded")
}

// --- promptWatcher: falls back to the human ---

// headlineRule detects the dialog by its headline but accepts an option label
// that is not on screen — the shape of a future agent release renaming its
// affirmative option.
func headlineRule() []rule {
	return []rule{{
		name:   "renamed-option",
		match:  "quick safety check",
		accept: "some option label that does not exist",
	}}
}

func TestPromptWatcher_LeavesPromptUntouchedWhenAcceptOptionMissing(t *testing.T) {
	sel := currentTrustSelect()
	w := newPromptWatcher(sel.Screen, sel, headlineRule(), 2*time.Millisecond, 60*time.Millisecond, nil)

	w.Run(context.Background())

	assert.Empty(t, sel.Keys(), "no keystrokes when the accepting option cannot be located")
	assert.Empty(t, sel.Confirmed(), "dialog is left unanswered for a human")
}

func TestPromptWatcher_LogsWhenPromptLeftForHuman(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	sel := currentTrustSelect()
	w := newPromptWatcher(sel.Screen, sel, headlineRule(), 2*time.Millisecond, 60*time.Millisecond, log)

	w.Run(context.Background())

	assert.Contains(t, logBuf.String(), "prompt_left_for_human",
		"operators are told a prompt needs a manual answer")
	assert.Contains(t, logBuf.String(), "renamed-option", "the unanswered rule is named")
}

func TestPromptWatcher_LogsPromptLeftForHumanAfterGivingUpNavigating(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	stdin := &lockedBuffer{}
	frozen := func() []string {
		return []string{"❯ No, exit", "  Yes, I trust this folder"}
	}
	w := newPromptWatcher(frozen, stdin, defaultPromptRules(), time.Millisecond, 10*time.Second, log)

	w.Run(context.Background())

	assert.Contains(t, logBuf.String(), "prompt_left_for_human",
		"abandoning navigation also reports that a human must answer")
}
