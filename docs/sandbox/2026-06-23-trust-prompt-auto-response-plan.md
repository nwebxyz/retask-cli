# Reliable Trust-Prompt Auto-Response Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make retask-cli reliably auto-accept Claude Code's startup folder-trust dialog by detecting it against the rendered terminal screen instead of the raw byte stream, and keep the session terminal emulator faithfully sized to the PTY.

**Architecture:** Two repos. In **agentfleet** the per-session `vt10x` emulator is sized from the PTY dimensions and resized whenever the PTY resizes, so `Runner.Lines()` mirrors what the agent actually drew. In **retask-cli** a polling `promptWatcher` reads `Runner.Lines()` every 500ms, matches known prompts against the *rendered* screen, and injects the accept keystroke once (fire-once); session PTYs start at 80×90.

**Tech Stack:** Go 1.26, `github.com/hinshun/vt10x` (terminal emulator), `github.com/stretchr/testify` (assertions), `cobra` (CLI), `coder/websocket`.

---

## Repo layout & dependency bridge

- **agentfleet:** `/Users/tan/nweb/opensource/agentfleet` — module `github.com/hoaitan/agentfleet`, currently tagged `v0.6.26`.
- **retask-cli:** `/Users/tan/nweb/retask-cli` — module `github.com/nwebxyz/retask-cli`, pins `github.com/hoaitan/agentfleet v0.6.26` with **no `replace` directive**.

No exported agentfleet signature changes, so retask-cli keeps compiling against `v0.6.26` throughout Phase 2. Phase 1 is completed, tagged `v0.6.27`, and pushed; then Phase 2 bumps the dependency to pick up the runtime behavior. Do Phase 1 fully before the bridge, then Phase 2.

## File Structure

**agentfleet (Phase 1):**
- Modify `vte.go` — add `vteHook.Resize(cols, rows int)`.
- Create `vte_test.go` — white-box (`package agentfleet`) test for the emulator resize.
- Modify `runner.go` — `NewRunner` sizes the emulator from PTY dims (VTERows fallback); `Runner.Resize` resizes the emulator too.
- Modify `runner_test.go` — black-box (`package agentfleet_test`) tests for resize + init sizing.
- Modify `tui/tui.go` — extract `previewLines` helper (locks the ≤5-row preview invariant).
- Create `tui/preview_test.go` — `package tui` test for `previewLines`.

**retask-cli (Phase 2):**
- Create `internal/cmd/sandbox/promptWatcher.go` — full prompt subsystem (normalize, rules, watcher).
- Delete `internal/cmd/sandbox/promptResponder.go`.
- Create `internal/cmd/sandbox/promptWatcher_test.go` — replaces `promptResponder_test.go`.
- Delete `internal/cmd/sandbox/promptResponder_test.go`.
- Modify `internal/cmd/sandbox/sessionlane.go` — launch the watcher instead of wrapping output.
- Modify `internal/cmd/sandbox/connect.go` — `sessionAgentConfig()` helper + 80×90 constants.
- Create `internal/cmd/sandbox/connect_test.go` (or append) — test for `sessionAgentConfig`.
- Modify `go.mod` / `go.sum` — bump agentfleet to `v0.6.27`.

---

# Phase 1 — agentfleet: emulator tracks the PTY

- [ ] **Step 0: Branch**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git checkout -b fix/vte-track-pty-size
git status   # expect: clean working tree on new branch
```

## Task 1: `vteHook.Resize`

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/vte.go`
- Test: `/Users/tan/nweb/opensource/agentfleet/vte_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `vte_test.go`:

```go
package agentfleet

import (
	"testing"

	"github.com/hoaitan/agentfleet/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Resizing the emulator changes its effective width: at 10 cols a 20-char line
// wraps; after widening to 20 cols the same 20 chars fit on one row.
func TestVTEHookResizeChangesWidth(t *testing.T) {
	h := newVTEHook(10, 4) // cols=10, rows=4

	_, err := h.Process([]byte("abcdefghijKLMNO"), hook.DirOut) // 15 chars > 10
	require.NoError(t, err)
	got := h.Screen()
	require.GreaterOrEqual(t, len(got), 2, "15 chars must wrap at width 10")
	assert.Equal(t, "abcdefghij", got[0])

	h.Resize(20, 4) // widen to 20 cols
	_, err = h.Process([]byte("\x1b[2J\x1b[Habcdefghijklmnopqrst"), hook.DirOut) // clear, home, 20 chars
	require.NoError(t, err)
	got = h.Screen()
	require.NotEmpty(t, got)
	assert.Equal(t, "abcdefghijklmnopqrst", got[0], "20 chars must fit on one row at width 20")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./ -run TestVTEHookResizeChangesWidth`
Expected: FAIL — `h.Resize undefined (type *vteHook has no field or method Resize)`.

- [ ] **Step 3: Add the `Resize` method**

In `vte.go`, after the `Screen()` method, add:

```go
// Resize changes the emulator's dimensions so the rendered screen keeps
// mirroring the PTY after a window-size change. vt10x.Terminal.Resize acquires
// its own internal mutex, so holding h.mu here is safe (it does not re-enter
// h.mu).
func (h *vteHook) Resize(cols, rows int) {
	h.mu.Lock()
	h.term.Resize(cols, rows)
	h.cols = cols
	h.rows = rows
	h.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./ -run TestVTEHookResizeChangesWidth`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add vte.go vte_test.go
git commit -m "feat(vte): add vteHook.Resize to track PTY size"
```

## Task 2: `Runner.Resize` resizes the emulator too

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/runner.go:215`
- Test: `/Users/tan/nweb/opensource/agentfleet/runner_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `runner_test.go`:

```go
func TestRunnerResizeResizesVTE(t *testing.T) {
	ag := agentfleet.NewMockAgent()
	task := &agentfleet.BasicTask{TaskID: "rz", TaskName: "Resize", Cmd: "echo"}
	// Start narrow: 10 cols.
	r := agentfleet.NewRunner(task, ag, testCfg(), agentfleet.AgentConfig{PTYCols: 10, PTYRows: 4})
	r.Start()

	require.NoError(t, ag.SimulateOutput([]byte("abcdefghijKLMNO"))) // 15 chars wrap at width 10
	time.Sleep(50 * time.Millisecond)
	lines := r.Lines()
	require.GreaterOrEqual(t, len(lines), 2)
	assert.Equal(t, "abcdefghij", lines[0], "confirms initial width 10")

	// Resize takes (rows, cols); the emulator must widen to 20.
	require.NoError(t, r.Resize(4, 20))
	require.NoError(t, ag.SimulateOutput([]byte("\x1b[2J\x1b[Habcdefghijklmnopqrst"))) // 20 chars
	time.Sleep(50 * time.Millisecond)
	lines = r.Lines()
	require.NotEmpty(t, lines)
	assert.Equal(t, "abcdefghijklmnopqrst", lines[0], "emulator widened to 20 (also proves rows/cols arg order)")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./ -run TestRunnerResizeResizesVTE`
Expected: FAIL — second assertion: `lines[0]` is `"abcdefghij"` (still width 10) instead of the full 20-char string, because the emulator was never resized.

- [ ] **Step 3: Resize the emulator inside `Runner.Resize`**

In `runner.go`, replace:

```go
// Resize resizes the underlying PTY agent.
func (r *Runner) Resize(rows, cols int) error { return r.ag.Resize(rows, cols) }
```

with:

```go
// Resize resizes both the underlying PTY agent and the virtual terminal
// emulator so Lines() keeps mirroring the agent's actual screen. Note the
// argument order: the PTY/agent take (rows, cols); vt10x takes (cols, rows).
func (r *Runner) Resize(rows, cols int) error {
	r.vte.Resize(cols, rows)
	return r.ag.Resize(rows, cols)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./ -run TestRunnerResizeResizesVTE`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add runner.go runner_test.go
git commit -m "feat(runner): resize the VTE alongside the PTY"
```

## Task 3: `NewRunner` sizes the emulator from the PTY dims

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/runner.go:93-111`
- Test: `/Users/tan/nweb/opensource/agentfleet/runner_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `runner_test.go`:

```go
// With PTYRows set, the emulator is exactly that tall: a cursor move to row 50
// is clamped to the PTY height (<=6 rows of rendered screen), not the 200-row
// FleetConfig.VTERows.
func TestNewRunnerVTESizedFromPTYRows(t *testing.T) {
	ag := agentfleet.NewMockAgent()
	task := &agentfleet.BasicTask{TaskID: "sz", TaskName: "Size", Cmd: "echo"}
	r := agentfleet.NewRunner(task, ag, agentfleet.FleetConfig{VTERows: 200}, agentfleet.AgentConfig{PTYCols: 12, PTYRows: 6})
	r.Start()

	require.NoError(t, ag.SimulateOutput([]byte("\x1b[50;1Hmark"))) // row 50 on a 6-row screen
	time.Sleep(50 * time.Millisecond)
	lines := r.Lines()
	assert.LessOrEqual(t, len(lines), 6, "cursor clamped to the 6-row PTY height, not 200")
}

// With PTYRows unset (<=0), fall back to FleetConfig.VTERows so existing callers
// that rely on a tall preview emulator keep working.
func TestNewRunnerVTEFallsBackToVTERows(t *testing.T) {
	ag := agentfleet.NewMockAgent()
	task := &agentfleet.BasicTask{TaskID: "fb", TaskName: "Fallback", Cmd: "echo"}
	r := agentfleet.NewRunner(task, ag, agentfleet.FleetConfig{VTERows: 200}, agentfleet.AgentConfig{PTYCols: 12, PTYRows: 0})
	r.Start()

	require.NoError(t, ag.SimulateOutput([]byte("\x1b[50;1Hmark"))) // row 50 valid in a 200-row screen
	time.Sleep(50 * time.Millisecond)
	lines := r.Lines()
	require.Len(t, lines, 50, "200-row emulator keeps row 50")
	assert.Equal(t, "mark", lines[49])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./ -run 'TestNewRunnerVTE'`
Expected: FAIL — `TestNewRunnerVTESizedFromPTYRows` returns 50 lines (emulator still 200 rows tall because `NewRunner` currently uses `cfg.VTERows` for the height).

- [ ] **Step 3: Size the emulator from PTY dims with VTERows fallback**

In `runner.go` `NewRunner`, replace:

```go
	vteRows := cfg.VTERows
	if vteRows <= 0 {
		vteRows = 200
	}
	vteCols := agentCfg.PTYCols
	if vteCols <= 0 {
		vteCols = 220
	}
```

with:

```go
	// The emulator mirrors the PTY, so size it from the PTY dims. Fall back to
	// FleetConfig.VTERows (then a sane default) only when PTYRows is unset, so
	// existing callers that don't set PTYRows keep their tall preview emulator.
	vteRows := agentCfg.PTYRows
	if vteRows <= 0 {
		vteRows = cfg.VTERows
	}
	if vteRows <= 0 {
		vteRows = 200
	}
	vteCols := agentCfg.PTYCols
	if vteCols <= 0 {
		vteCols = 220
	}
```

- [ ] **Step 4: Run the full agentfleet suite**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./...`
Expected: PASS (new sizing tests pass; existing tests still pass because their `AgentConfig{}` has `PTYRows == 0`, hitting the VTERows fallback).

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add runner.go runner_test.go
git commit -m "feat(runner): size the VTE from PTY dims with VTERows fallback"
```

## Task 4: Lock the ≤5-row preview invariant

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/tui/tui.go:486-519`
- Test: `/Users/tan/nweb/opensource/agentfleet/tui/preview_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `tui/preview_test.go`:

```go
package tui

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPreviewLines_CapsAtN(t *testing.T) {
	in := make([]string, 50)
	for i := range in {
		in[i] = fmt.Sprintf("line%d", i)
	}
	out := previewLines(in, 5)
	assert.Len(t, out, 5, "never more than n rows regardless of emulator height")
	assert.Equal(t, "line45", out[0])
	assert.Equal(t, "line49", out[4])
}

func TestPreviewLines_FewerThanN(t *testing.T) {
	assert.Len(t, previewLines([]string{"a", "b"}, 5), 2)
	assert.Empty(t, previewLines(nil, 5))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./tui -run TestPreviewLines`
Expected: FAIL — `undefined: previewLines`.

- [ ] **Step 3: Extract the helper and use it**

In `tui/tui.go`, add this function (place it just above `renderCard`):

```go
// previewLines returns the last n filtered lines for a task card preview,
// ANSI-stripped. The cap is independent of the emulator height, so the card
// never grows when the session terminal is large.
func previewLines(lines []string, n int) []string {
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, n)
	for _, l := range lines[start:] {
		out = append(out, stripANSI(l))
	}
	return out
}
```

Then in the selected-row block, replace:

```go
		selected := row.idx == m.cursor
		var preview []string
		if selected {
			filtered := filter(row.runner.Lines())
			start := len(filtered) - previewN
			if start < 0 {
				start = 0
			}
			for _, l := range filtered[start:] {
				preview = append(preview, stripANSI(l))
			}
		}
```

with:

```go
		selected := row.idx == m.cursor
		var preview []string
		if selected {
			preview = previewLines(filter(row.runner.Lines()), previewN)
		}
```

- [ ] **Step 4: Run the TUI tests**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go test ./tui`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add tui/tui.go tui/preview_test.go
git commit -m "refactor(tui): extract previewLines to lock the 5-row cap"
```

---

# Bridge — release agentfleet v0.6.27

- [ ] **Step 1: Full suite + vet**

Run: `cd /Users/tan/nweb/opensource/agentfleet && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 2: Merge to main and tag**

The module dependency must resolve `v0.6.27`, so the tag has to be on a pushed commit (conventionally `main`).

```bash
cd /Users/tan/nweb/opensource/agentfleet
git checkout main
git merge --no-ff fix/vte-track-pty-size -m "Merge: VTE tracks PTY size"
git tag v0.6.27
git push origin main --tags
```

> Pushing is outbound and requires push access to `github.com/hoaitan/agentfleet`. Confirm before running. If you prefer not to merge yet, tag the feature branch head instead and push that branch + tag.

---

# Phase 2 — retask-cli: screen-based watcher + 80×90 PTY

- [ ] **Step 0: Branch is already `fix/sandbox-trust-prompt-auto-response`**

```bash
cd /Users/tan/nweb/retask-cli
git branch --show-current   # expect: fix/sandbox-trust-prompt-auto-response
```

## Task 5: Bump agentfleet to v0.6.27

**Files:**
- Modify: `/Users/tan/nweb/retask-cli/go.mod`, `/Users/tan/nweb/retask-cli/go.sum`

- [ ] **Step 1: Update the dependency**

```bash
cd /Users/tan/nweb/retask-cli
go get github.com/hoaitan/agentfleet@v0.6.27
go mod tidy
```

- [ ] **Step 2: Verify the build**

Run: `cd /Users/tan/nweb/retask-cli && go build ./... && go test ./...`
Expected: PASS (no code change yet; this only confirms the new version resolves and the existing tree still builds).

- [ ] **Step 3: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add go.mod go.sum
git commit -m "build: bump agentfleet to v0.6.27 (VTE tracks PTY size)"
```

## Task 6: Replace `promptResponder` with a screen-polling `promptWatcher`

This is a 1:1 component replacement: the new watcher reuses `normalize`, `rule`, and `defaultPromptRules`, which move into the new file. Because the old and new components would otherwise define the same symbols, do the swap as one coordinated change and gate on the package test run.

**Files:**
- Create: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/promptWatcher.go`
- Delete: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/promptResponder.go`
- Create: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/promptWatcher_test.go`
- Delete: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/promptResponder_test.go`
- Modify: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/sessionlane.go:140-148`

- [ ] **Step 1: Create `promptWatcher.go`**

```go
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
```

- [ ] **Step 2: Delete the old responder**

```bash
cd /Users/tan/nweb/retask-cli
git rm internal/cmd/sandbox/promptResponder.go internal/cmd/sandbox/promptResponder_test.go
```

- [ ] **Step 3: Create `promptWatcher_test.go`**

```go
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
```

- [ ] **Step 4: Rewire `sessionlane.go`**

In `sessionlane.go`, replace:

```go
	var out io.Writer = &wsWriter{ctx: ctx, conn: wsConn}
	if sm.autoRespond {
		// Watch the PTY stream for known startup prompts (e.g. Claude Code's
		// folder-trust dialog) and inject the accept keystroke, so unattended
		// sessions don't stall waiting for a human. Degrades to pass-through
		// once every rule has fired.
		out = newPromptResponder(out, r.StdinWriter(), defaultPromptRules(), defaultInjectDelay, sm.log)
	}
	r.SetOutput(out)
```

with:

```go
	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})
	if sm.autoRespond {
		// Watch the session's rendered screen (via the emulator) for known
		// startup prompts (e.g. Claude Code's folder-trust dialog) and inject
		// the accept keystroke once, so unattended sessions don't stall waiting
		// for a human. Stops once every rule has fired or the watch window ends.
		go newPromptWatcher(r.Lines, r.StdinWriter(), defaultPromptRules(), defaultPollInterval, defaultPromptWindow, sm.log).Run(ctx)
	}
```

- [ ] **Step 5: Check the `io` import in `sessionlane.go`**

`io` may now be unused. Run: `cd /Users/tan/nweb/retask-cli && go build ./internal/cmd/sandbox/`
If it fails with `"io" imported and not used`, remove the `"io"` line from the import block in `sessionlane.go`. (The `wsWriter.Write` method signature uses no `io` symbol; `var out io.Writer` was the only user.) Re-run the build until it passes.

- [ ] **Step 6: Run the package tests**

Run: `cd /Users/tan/nweb/retask-cli && go test ./internal/cmd/sandbox/...`
Expected: PASS — normalize tests, rule test, and all `promptWatcher` tests green; no references to the deleted `promptResponder` remain.

- [ ] **Step 7: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/
git commit -m "feat(sandbox): detect startup prompts on the rendered screen

Replace the raw-byte-stream promptResponder with a promptWatcher that
polls Runner.Lines() (the vt10x-rendered screen) every 500ms and injects
the accept keystroke once. TUI agents draw with cursor positioning, so the
prompt text never appears contiguously in the raw stream — the old detector
could not match it."
```

## Task 7: Start session PTYs at 80×90

**Files:**
- Modify: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect.go:31-34, 128`
- Test: `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `connect_test.go`:

```go
package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionAgentConfig_Is80x90(t *testing.T) {
	a := sessionAgentConfig()
	assert.Equal(t, 80, a.PTYCols, "session terminal width")
	assert.Equal(t, 90, a.PTYRows, "session terminal height")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tan/nweb/retask-cli && go test ./internal/cmd/sandbox/ -run TestSessionAgentConfig`
Expected: FAIL — `undefined: sessionAgentConfig`.

- [ ] **Step 3: Add the helper and constants**

In `connect.go`, add near the top of the file (after the `import` block):

```go
// Session PTY defaults. Sessions start at a generously sized, conventional
// terminal so the agent's TUI (e.g. Claude Code's trust dialog) renders fully
// for the screen watcher to detect; a 90-row height keeps the whole startup
// render on screen. A later session_resize from an attached client corrects the
// size, which now also moves the emulator (agentfleet >= v0.6.27).
const (
	sessionPTYCols = 80
	sessionPTYRows = 90
)

// sessionAgentConfig returns the agentfleet AgentConfig used for session PTYs.
func sessionAgentConfig() agentfleet.AgentConfig {
	a := agentfleet.DefaultConfig().Agent
	a.PTYCols = sessionPTYCols
	a.PTYRows = sessionPTYRows
	return a
}
```

Then, in the `RunE` body where the fleet config is built, replace:

```go
			fleetCfg := agentfleet.DefaultConfig()
```

with:

```go
			fleetCfg := agentfleet.DefaultConfig()
			fleetCfg.Agent = sessionAgentConfig()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tan/nweb/retask-cli && go test ./internal/cmd/sandbox/ -run TestSessionAgentConfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/connect.go internal/cmd/sandbox/connect_test.go
git commit -m "feat(sandbox): start session PTYs at 80x90"
```

## Task 8: Full verification

- [ ] **Step 1: Build + vet + test both repos**

```bash
cd /Users/tan/nweb/retask-cli && go vet ./... && go test ./... && go build -o /tmp/retask ./cmd/retask/
cd /Users/tan/nweb/opensource/agentfleet && go vet ./... && go test ./...
```
Expected: all PASS; `/tmp/retask` builds.

- [ ] **Step 2: Manual end-to-end (live sandbox)**

Against a PRIVATE sandbox whose session launches Claude Code:

```bash
/tmp/retask sandbox connect <sandbox-id> --mode headless
```

Then create/start a session that runs `claude`. Confirm in the streamed logs:
- `prompt_detected rule=claude-trust ...` appears **without** any manual keypress, followed by
- `prompt_autoresponded rule=claude-trust`, and
- the trust dialog is dismissed automatically (the session proceeds to Claude's prompt).

Also confirm a browser attach + resize: the session resizes and the agent re-renders cleanly (no garbled screen), and the TUI task card preview stays ≤5 rows.

> Optional: use the `verify` skill to drive this run and capture evidence.

- [ ] **Step 3: Finishing the branch**

Use the `superpowers:finishing-a-development-branch` skill to choose merge/PR/cleanup for both the agentfleet and retask-cli branches.

---

## Self-Review

**Spec coverage:**
- Part 1 (detect against rendered screen, 500ms poll, fire-once, 120s window) → Task 6. ✓
- Part 2 (`vteHook.Resize`, `Runner.Resize` resizes VTE, `NewRunner` sizes from PTY with VTERows fallback) → Tasks 1–3. ✓
- Part 3 (80×90 init in `connect.go`) → Task 7. ✓
- Part 4 (preview ≤5 rows invariant) → Task 4. ✓
- Cross-repo dependency (no `replace`; tag + bump) → Bridge + Task 5. ✓
- Acceptance criteria 1–5 → Task 8 (manual) + per-task `go test`. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command states expected output. ✓

**Type/identifier consistency:** `newPromptWatcher(screen, stdin, rules, interval, window, log)` defined in Task 6 and called with the same arity in `sessionlane.go` (Task 6) — `r.Lines` (screen), `r.StdinWriter()` (stdin), `defaultPromptRules()`, `defaultPollInterval`, `defaultPromptWindow`, `sm.log`. `vteHook.Resize(cols, rows)` (Task 1) called as `r.vte.Resize(cols, rows)` (Task 2). `previewLines(lines, n)` defined and called in Task 4. `sessionAgentConfig()` defined and tested in Task 7. ✓
