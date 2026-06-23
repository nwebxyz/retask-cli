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
	// The rendered trust-option line, as the emulator would surface it.
	norm := normalize([]byte("❯ 1. Yes, I trust this folder"))
	rules := defaultPromptRules()
	assert.NotEmpty(t, rules)
	for _, r := range rules {
		if r.name == "claude-trust" {
			assert.True(t, strings.Contains(norm, r.match),
				"claude-trust match %q not found in %q", r.match, norm)
		}
	}
}

// --- promptWatcher ---

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

// trustScreen is a rendered (post-emulator) screen showing the trust dialog.
func trustScreen() []string {
	return []string{
		"Quick safety check: Is this a project you created or one you trust?",
		"",
		"❯ 1. Yes, I trust this folder",
		"  2. No, exit",
	}
}

func TestPromptWatcher_FiresOnTrustScreen(t *testing.T) {
	stdin := &lockedBuffer{}
	w := newPromptWatcher(trustScreen, stdin, defaultPromptRules(), 5*time.Millisecond, time.Second, nil)

	done := make(chan struct{})
	go func() { w.Run(context.Background()); close(done) }()

	assert.Eventually(t, func() bool { return stdin.String() == "\r" },
		time.Second, 5*time.Millisecond, "accept keystroke injected on the rendered trust screen")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after all rules fired")
	}
}

func TestPromptWatcher_FiresOnce(t *testing.T) {
	stdin := &lockedBuffer{}
	// Screen always shows the dialog; the watcher must still inject exactly once.
	w := newPromptWatcher(trustScreen, stdin, defaultPromptRules(), 2*time.Millisecond, time.Second, nil)
	w.Run(context.Background()) // returns once the single rule fires

	assert.Equal(t, "\r", stdin.String(), "fired exactly once")
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
	stdin := &lockedBuffer{}
	w := newPromptWatcher(trustScreen, stdin, nil, 5*time.Millisecond, time.Hour, nil)
	w.Run(context.Background()) // must not block
	assert.Empty(t, stdin.String())
}

func TestPromptWatcher_LogsDetectionAndResponse(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	stdin := &bytes.Buffer{} // single-goroutine: Run writes and returns before we read
	w := newPromptWatcher(trustScreen, stdin, defaultPromptRules(), 2*time.Millisecond, time.Second, log)

	w.Run(context.Background())

	logs := logBuf.String()
	assert.Contains(t, logs, "prompt_detected", "detection is logged")
	assert.Contains(t, logs, "prompt_autoresponded", "keystroke injection is logged")
	assert.Contains(t, logs, "claude-trust", "the fired rule is named in the logs")
}
