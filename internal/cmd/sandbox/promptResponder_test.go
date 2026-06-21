package sandbox

import (
	"bytes"
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

// A realistic, noisy render of Claude Code's startup folder-trust dialog:
// ANSI escapes, box-drawing borders, and hard-wrapped lines.
const trustPromptRaw = "\x1b[2J\x1b[H\x1b[1m╭─────────────────────────────────────────────╮\x1b[0m\r\n" +
	"│ Quick safety check: Is this a project you   │\r\n" +
	"│ created or one you trust?                   │\r\n" +
	"│                                             │\r\n" +
	"│ (Like your own code, well-known open source │\r\n" +
	"│ project, or work from your team.) If not,   │\r\n" +
	"│ take a moment to review the files first.    │\r\n" +
	"│                                             │\r\n" +
	"│ \x1b[7m❯ 1. Yes, I trust this folder\x1b[0m              │\r\n" +
	"│   2. No, exit                               │\r\n" +
	"╰─────────────────────────────────────────────╯\r\n"

func newTestResponder(rules []rule) (pr *promptResponder, out, stdin *bytes.Buffer) {
	out = &bytes.Buffer{}
	stdin = &bytes.Buffer{}
	pr = newPromptResponder(out, stdin, rules, 0, nil) // 0 delay: inject synchronously in tests
	return pr, out, stdin
}

// --- promptResponder ---

func TestPromptResponder_InjectsOnTrustPrompt(t *testing.T) {
	pr, out, stdin := newTestResponder(defaultPromptRules())

	n, err := pr.Write([]byte(trustPromptRaw))

	assert.NoError(t, err)
	assert.Equal(t, len(trustPromptRaw), n)
	assert.Equal(t, "\r", stdin.String(), "accept keystroke injected")
	assert.Equal(t, trustPromptRaw, out.String(), "output passed through byte-identical")
}

func TestPromptResponder_PassesThroughUnaltered(t *testing.T) {
	pr, out, stdin := newTestResponder(defaultPromptRules())

	const data = "just some normal \x1b[32magent\x1b[0m output\r\n"
	pr.Write([]byte(data)) //nolint:errcheck

	assert.Equal(t, data, out.String())
	assert.Empty(t, stdin.String(), "no injection on unrelated output")
}

func TestPromptResponder_FiresOncePerRule(t *testing.T) {
	pr, _, stdin := newTestResponder(defaultPromptRules())

	pr.Write([]byte(trustPromptRaw)) //nolint:errcheck
	// The dialog text is still in the buffer; a redraw must not re-fire.
	pr.Write([]byte(trustPromptRaw)) //nolint:errcheck

	assert.Equal(t, "\r", stdin.String(), "fired exactly once")
}

func TestPromptResponder_SplitAcrossWrites(t *testing.T) {
	pr, _, stdin := newTestResponder(defaultPromptRules())

	// Split mid-phrase so the match ("trust this folder") straddles the two
	// writes; this exercises the rolling buffer accumulating a prompt across
	// Write calls.
	split := strings.Index(trustPromptRaw, "this folder")
	pr.Write([]byte(trustPromptRaw[:split])) //nolint:errcheck
	assert.Empty(t, stdin.String(), "no match before the option line completes")
	pr.Write([]byte(trustPromptRaw[split:])) //nolint:errcheck

	assert.Equal(t, "\r", stdin.String(), "fires once buffer accumulates the full prompt")
}

// Guards the timing regression: the headline and descriptive text alone must
// not trigger acceptance. We only press Enter once the interactive menu option
// has rendered — otherwise the keystroke lands on a half-drawn dialog and is
// dropped.
func TestPromptResponder_DoesNotFireBeforeMenuOption(t *testing.T) {
	pr, _, stdin := newTestResponder(defaultPromptRules())

	optionAt := strings.Index(trustPromptRaw, "I trust this folder")
	pr.Write([]byte(trustPromptRaw[:optionAt])) //nolint:errcheck
	assert.Empty(t, stdin.String(), "headline/description must not trigger acceptance")

	pr.Write([]byte(trustPromptRaw[optionAt:])) //nolint:errcheck
	assert.Equal(t, "\r", stdin.String(), "accepts once the trust option is on screen")
}

// lockedBuffer is a goroutine-safe io.Writer for asserting on output that the
// inject-delay timer writes from a separate goroutine.
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

func TestPromptResponder_InjectDelayDefersKeystroke(t *testing.T) {
	out := &bytes.Buffer{}
	stdin := &lockedBuffer{}
	pr := newPromptResponder(out, stdin, defaultPromptRules(), 40*time.Millisecond, nil)

	pr.Write([]byte(trustPromptRaw)) //nolint:errcheck

	// The match fired, but the keystroke is held back until the delay elapses.
	assert.Empty(t, stdin.String(), "keystroke deferred by injectDelay")
	assert.Eventually(t, func() bool { return stdin.String() == "\r" },
		time.Second, 5*time.Millisecond, "keystroke injected after the delay")
}

func TestPromptResponder_EmptyRulesBypass(t *testing.T) {
	pr, out, stdin := newTestResponder(nil)

	pr.Write([]byte(trustPromptRaw)) //nolint:errcheck

	assert.Empty(t, stdin.String(), "no rules: nothing injected")
	assert.Equal(t, trustPromptRaw, out.String(), "output still passes through")
}

func TestDefaultPromptRules_MatchesNormalizedTrustText(t *testing.T) {
	// Guards against the rule's match string drifting from the actual dialog.
	norm := normalize([]byte(trustPromptRaw))
	rules := defaultPromptRules()
	assert.NotEmpty(t, rules)
	for _, r := range rules {
		if r.name == "claude-trust" {
			assert.True(t, strings.Contains(norm, r.match),
				"claude-trust match %q not found in %q", r.match, norm)
		}
	}
}
