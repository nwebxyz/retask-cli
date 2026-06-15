# sandbox connect TUI redesign + agentfleet v0.6.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign agentfleet's TUI to a split-pane layout (task list left, output right, log panel bottom), publish v0.6.0, then wire the new `LogBuffer` API into `retask sandbox connect`.

**Architecture:** Phase 1 (agentfleet): add `LogBuffer` exported type, update `TUIConfig` (remove card fields, add `MaxDoneTasks`/`Log`), add `tui/ordered.go` helper, fully rewrite `tui/tui.go` rendering. Phase 2 (retask-cli): bump dep to v0.6.0 and wire `LogBuffer` into `connect.go`.

**Tech Stack:** Go 1.26, Bubbletea v1.3, Lipgloss v1.1, slog (stdlib), testify

---

## File Map

**agentfleet** (`/Users/tan/nweb/opensource/agentfleet`):

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `logbuffer.go` | `LogBuffer` type — thread-safe `io.Writer` + `Lines()` ring buffer |
| Create | `logbuffer_test.go` | Unit tests for `LogBuffer` |
| Modify | `config.go` | Remove `Columns`/`PreviewLines`/`CardWidth`; add `MaxDoneTasks int`, `Log *LogBuffer` |
| Create | `tui/ordered.go` | `orderedRunners()` — pure helper splitting runners into active/done sections |
| Create | `tui/ordered_test.go` | Unit tests for `orderedRunners` |
| Modify | `tui/tui.go` | Full rewrite: new `model` fields, `Update()` scroll handling, `View()` split-pane |

**retask-cli** (`/Users/tan/nweb/retask-cli`):

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `go.mod` + `go.sum` | Bump `github.com/hoaitan/agentfleet` to `v0.6.0` |
| Modify | `internal/cmd/sandbox/connect.go` | Create `LogBuffer`, always-wire logger, set `TUI.Log` in TUI mode |

---

## Phase 1: agentfleet v0.6.0

### Task 1: LogBuffer type

**Files:**
- Create: `logbuffer.go`
- Create: `logbuffer_test.go`

- [ ] **Step 1: Write failing tests**

Create `/Users/tan/nweb/opensource/agentfleet/logbuffer_test.go`:

```go
package agentfleet_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	agentfleet "github.com/hoaitan/agentfleet"
)

func TestLogBuffer_Write_basic(t *testing.T) {
	lb := agentfleet.NewLogBuffer(10)
	lb.Write([]byte("line1\nline2\n")) //nolint:errcheck
	assert.Equal(t, []string{"line1", "line2"}, lb.Lines())
}

func TestLogBuffer_Write_partialThenComplete(t *testing.T) {
	lb := agentfleet.NewLogBuffer(10)
	lb.Write([]byte("hel"))        //nolint:errcheck
	lb.Write([]byte("lo\nworld\n")) //nolint:errcheck
	assert.Equal(t, []string{"hello", "world"}, lb.Lines())
}

func TestLogBuffer_Overflow_dropsOldest(t *testing.T) {
	lb := agentfleet.NewLogBuffer(3)
	lb.Write([]byte("a\nb\nc\nd\n")) //nolint:errcheck
	assert.Equal(t, []string{"b", "c", "d"}, lb.Lines())
}

func TestLogBuffer_Lines_returnsCopy(t *testing.T) {
	lb := agentfleet.NewLogBuffer(10)
	lb.Write([]byte("x\n")) //nolint:errcheck
	lines := lb.Lines()
	lines[0] = "mutated"
	assert.Equal(t, []string{"x"}, lb.Lines()) // original unchanged
}

func TestLogBuffer_ImplementsWriter(t *testing.T) {
	lb := agentfleet.NewLogBuffer(10)
	n, err := lb.Write([]byte("test\n"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestLogBuffer_ConcurrentWrite(t *testing.T) {
	lb := agentfleet.NewLogBuffer(100)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			lb.Write([]byte(strings.Repeat("a", 10) + "\n")) //nolint:errcheck
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	assert.LessOrEqual(t, len(lb.Lines()), 100)
}
```

- [ ] **Step 2: Run tests — expect compile failure**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./... -run TestLogBuffer 2>&1 | head -20
```

Expected: `undefined: agentfleet.NewLogBuffer`

- [ ] **Step 3: Create logbuffer.go**

Create `/Users/tan/nweb/opensource/agentfleet/logbuffer.go`:

```go
package agentfleet

import (
	"bytes"
	"sync"
)

// LogBuffer is a thread-safe line-oriented ring buffer that implements io.Writer.
// Pass it to slog.NewTextHandler to capture log output; read Lines() in the TUI.
type LogBuffer struct {
	mu    sync.RWMutex
	lines []string
	max   int
	acc   []byte
}

// NewLogBuffer returns a LogBuffer keeping at most maxLines lines.
func NewLogBuffer(maxLines int) *LogBuffer {
	if maxLines <= 0 {
		maxLines = 200
	}
	return &LogBuffer{max: maxLines}
}

// Write implements io.Writer. Lines are split on '\n'; incomplete lines are
// buffered until the next Write that completes them.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acc = append(b.acc, p...)
	for {
		idx := bytes.IndexByte(b.acc, '\n')
		if idx < 0 {
			break
		}
		line := string(b.acc[:idx])
		b.acc = b.acc[idx+1:]
		if len(b.lines) >= b.max {
			b.lines = b.lines[1:]
		}
		b.lines = append(b.lines, line)
	}
	return len(p), nil
}

// Lines returns a snapshot of buffered lines, oldest first.
func (b *LogBuffer) Lines() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}
```

- [ ] **Step 4: Run tests — expect all pass**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./... -run TestLogBuffer -v 2>&1
```

Expected: all 6 `TestLogBuffer_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet && git add logbuffer.go logbuffer_test.go && git commit -m "feat: add LogBuffer — thread-safe io.Writer ring buffer for TUI log panel"
```

---

### Task 2: Update TUIConfig

**Files:**
- Modify: `config.go`

- [ ] **Step 1: Update config.go**

In `/Users/tan/nweb/opensource/agentfleet/config.go`, replace the `TUIConfig` struct and update `DefaultConfig()`:

Old `TUIConfig`:
```go
type TUIConfig struct {
	Title        func() string
	Columns      int
	PreviewLines int
	CardWidth    int
	RefreshRate  time.Duration
	AutoOpen     bool
}
```

New `TUIConfig`:
```go
// TUIConfig controls the Bubbletea dashboard appearance.
type TUIConfig struct {
	Title        func() string // header title func; nil = "◈ agentfleet"
	RefreshRate  time.Duration // TUI tick interval          — default: 500ms
	AutoOpen     bool          // auto-open a tab for each task when it starts — default: true
	MaxDoneTasks int           // done/failed tasks kept in list; 0 = no limit — default: 10
	Log          *LogBuffer    // nil = no log panel
}
```

Old `DefaultConfig()` TUI section:
```go
		TUI: TUIConfig{
			Columns:      3,
			PreviewLines: 3,
			CardWidth:    64,
			RefreshRate:  500 * time.Millisecond,
			AutoOpen:     true,
		},
```

New:
```go
		TUI: TUIConfig{
			RefreshRate:  500 * time.Millisecond,
			AutoOpen:     true,
			MaxDoneTasks: 10,
		},
```

- [ ] **Step 2: Verify build**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go build ./... 2>&1
```

Expected: compile errors in `tui/tui.go` referencing `cfg.CardWidth`, `cfg.PreviewLines`, `cfg.Columns`. These will be fixed in Task 5.

- [ ] **Step 3: Commit config only**

```bash
cd /Users/tan/nweb/opensource/agentfleet && git add config.go && git commit -m "feat: update TUIConfig — remove card fields, add MaxDoneTasks and Log"
```

---

### Task 3: orderedRunners helper

**Files:**
- Create: `tui/ordered.go`
- Create: `tui/ordered_test.go`

- [ ] **Step 1: Write failing tests**

Create `/Users/tan/nweb/opensource/agentfleet/tui/ordered_test.go`:

```go
package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentfleet "github.com/hoaitan/agentfleet"
)

func makeTestRunner(id, name string) (*agentfleet.Runner, *agentfleet.MockAgent) {
	ag := agentfleet.NewMockAgent()
	task := &agentfleet.BasicTask{TaskID: id, TaskName: name}
	r := agentfleet.NewRunner(task, ag, agentfleet.FleetConfig{RingBufferSize: 10}, agentfleet.AgentConfig{})
	return r, ag
}

func waitDone(t *testing.T, r *agentfleet.Runner) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("runner did not finish in time")
	}
}

func TestOrderedRunners_ActiveNewestFirst(t *testing.T) {
	r1, ag1 := makeTestRunner("t1", "task1")
	r2, ag2 := makeTestRunner("t2", "task2")
	r3, _ := makeTestRunner("t3", "task3")

	r1.Start()
	r2.Start()
	r3.Start()
	defer ag2.Stop() //nolint:errcheck
	defer r3.Stop()  //nolint:errcheck

	// r1 finishes → StatusDone
	ag1.Stop() //nolint:errcheck
	waitDone(t, r1)

	// fleet order (oldest→newest): [r1, r2, r3]
	runners := []*agentfleet.Runner{r1, r2, r3}
	active, done := orderedRunners(runners, 10)

	// active newest-first: r3, r2
	require.Len(t, active, 2)
	assert.Equal(t, "t3", active[0].Task().ID())
	assert.Equal(t, "t2", active[1].Task().ID())

	// done: r1
	require.Len(t, done, 1)
	assert.Equal(t, "t1", done[0].Task().ID())
}

func TestOrderedRunners_MaxDoneTasks_keepsNewest(t *testing.T) {
	var runners []*agentfleet.Runner
	for i := 0; i < 5; i++ {
		r, ag := makeTestRunner(fmt.Sprintf("t%d", i), fmt.Sprintf("task%d", i))
		r.Start()
		ag.Stop() //nolint:errcheck
		waitDone(t, r)
		runners = append(runners, r)
	}

	// fleet order: t0(oldest)…t4(newest); all done
	_, done := orderedRunners(runners, 3)

	// keeps 3 newest: t4, t3, t2
	require.Len(t, done, 3)
	assert.Equal(t, "t4", done[0].Task().ID())
	assert.Equal(t, "t3", done[1].Task().ID())
	assert.Equal(t, "t2", done[2].Task().ID())
}

func TestOrderedRunners_MaxDoneZero_noLimit(t *testing.T) {
	var runners []*agentfleet.Runner
	for i := 0; i < 5; i++ {
		r, ag := makeTestRunner(fmt.Sprintf("t%d", i), fmt.Sprintf("task%d", i))
		r.Start()
		ag.Stop() //nolint:errcheck
		waitDone(t, r)
		runners = append(runners, r)
	}
	_, done := orderedRunners(runners, 0)
	assert.Len(t, done, 5)
}

func TestOrderedRunners_Empty(t *testing.T) {
	active, done := orderedRunners(nil, 10)
	assert.Empty(t, active)
	assert.Empty(t, done)
}
```

- [ ] **Step 2: Run tests — expect compile failure**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./tui/... -run TestOrderedRunners 2>&1 | head -10
```

Expected: `undefined: orderedRunners`

- [ ] **Step 3: Create tui/ordered.go**

Create `/Users/tan/nweb/opensource/agentfleet/tui/ordered.go`:

```go
package tui

import agentfleet "github.com/hoaitan/agentfleet"

// orderedRunners splits a fleet snapshot into:
//   active — running+pending, newest first (last in fleet = first in result)
//   done   — done+failed, newest first, capped at maxDone (0 = no limit)
func orderedRunners(runners []*agentfleet.Runner, maxDone int) (active, done []*agentfleet.Runner) {
	for i := len(runners) - 1; i >= 0; i-- {
		r := runners[i]
		switch r.Status() {
		case agentfleet.StatusRunning, agentfleet.StatusPending:
			active = append(active, r)
		default:
			done = append(done, r)
		}
	}
	if maxDone > 0 && len(done) > maxDone {
		done = done[:maxDone]
	}
	return
}
```

- [ ] **Step 4: Run tests — expect all pass**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./tui/... -run TestOrderedRunners -v 2>&1
```

Expected: all 4 `TestOrderedRunners_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet && git add tui/ordered.go tui/ordered_test.go && git commit -m "feat: add orderedRunners helper — active newest-first, done capped at MaxDoneTasks"
```

---

### Task 4: Rewrite tui/tui.go — model and Update()

**Files:**
- Modify: `tui/tui.go`

This task replaces the `model` struct and `Update()` function. `View()` and render functions are tackled in Tasks 5–6.

- [ ] **Step 1: Replace the model struct, styles, and Update()**

Replace the contents of `/Users/tan/nweb/opensource/agentfleet/tui/tui.go` with the following (keep the package declaration, imports, and terminal-opening functions `OpenInTerminal`, `openLinuxTerminal`, `defaultOnAttach` — only replace the TUI model/render sections):

Full new file content:

```go
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	agentfleet "github.com/hoaitan/agentfleet"
)

var ansiRe = regexp.MustCompile(
	`\x1b(?:` +
		`\][^\x07\x1b]*(?:\x07|\x1b\\)` +
		`|[@-Z\\-_]` +
		`|\[[0-?]*[ -/]*[@-~]` +
		`|[PX^_][^\x1b]*\x1b\\` +
		`)`,
)

func stripANSI(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 || r == '\t' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c084fc"))
	styleSummary = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	styleMeta    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	styleSelID   = lipgloss.NewStyle().Foreground(lipgloss.Color("#c084fc"))
	styleOutput  = lipgloss.NewStyle().Foreground(lipgloss.Color("#d1d5db"))
	styleFooter  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4b5563"))
	styleLog     = lipgloss.NewStyle().Foreground(lipgloss.Color("#4b5563"))
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399"))
	styleFailed  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
	stylePending = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	styleSel     = lipgloss.NewStyle().Background(lipgloss.Color("#1e1730"))
	styleDivider = lipgloss.NewStyle().Foreground(lipgloss.Color("#374151"))
)

type tickMsg struct{}
type ctxDoneMsg struct{}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func ctxDoneCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return ctxDoneMsg{}
	}
}

type model struct {
	fleet      *agentfleet.Fleet
	cfg        agentfleet.TUIConfig
	onAttach   func(taskID string)
	ctx        context.Context
	cursor     int
	termW      int
	termH      int
	openedTabs map[string]bool

	listOffset    int // first visible visual row in task list
	outScrollBack int // 0 = snapped to bottom; N = scrolled N lines up from bottom
}

// Run starts the Bubbletea TUI and blocks until the user quits or ctx is cancelled.
// onAttach is called when the user presses Enter on a running task.
// If onAttach is nil, the default behaviour opens an iTerm2 tab with the attach binary.
func Run(ctx context.Context, fleet *agentfleet.Fleet, cfg agentfleet.TUIConfig, onAttach func(taskID string)) error {
	if onAttach == nil {
		onAttach = defaultOnAttach
	}
	m := model{fleet: fleet, cfg: cfg, onAttach: onAttach, ctx: ctx, openedTabs: make(map[string]bool)}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func defaultOnAttach(taskID string) {
	attachBin, _ := filepath.Abs("./attach")
	OpenInTerminal(attachBin, taskID)
}

// OpenInTerminal opens a new terminal tab/window running the given command.
func OpenInTerminal(cmd ...string) {
	if len(cmd) == 0 {
		return
	}
	cmdStr := strings.Join(cmd, " ")
	if os.Getenv("TMUX") != "" {
		exec.Command("tmux", append([]string{"new-window"}, cmd...)...).Start() //nolint:errcheck
		return
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app":
		script := fmt.Sprintf("tell application \"iTerm2\"\ntell current window\ncreate tab with default profile command \"%s\"\nend tell\nend tell", cmdStr)
		exec.Command("osascript", "-e", script).Start() //nolint:errcheck
	case "Apple_Terminal":
		script := fmt.Sprintf("tell application \"Terminal\"\ndo script \"%s\"\nactivate\nend tell", cmdStr)
		exec.Command("osascript", "-e", script).Start() //nolint:errcheck
	case "ghostty":
		exec.Command("ghostty", append([]string{"-e"}, cmd...)...).Start() //nolint:errcheck
	default:
		if os.Getenv("TERM") == "xterm-kitty" {
			exec.Command("kitty", cmd...).Start() //nolint:errcheck
			return
		}
		openLinuxTerminal(cmd...)
	}
}

func openLinuxTerminal(cmd ...string) {
	cmdStr := strings.Join(cmd, " ")
	candidates := [][]string{
		append([]string{"gnome-terminal", "--"}, cmd...),
		append([]string{"xterm", "-e"}, cmd...),
		append([]string{"alacritty", "-e"}, cmd...),
		append([]string{"konsole", "-e"}, cmd...),
		{"xfce4-terminal", "-e", cmdStr},
	}
	for _, args := range candidates {
		if _, err := exec.LookPath(args[0]); err == nil {
			exec.Command(args[0], args[1:]...).Start() //nolint:errcheck
			return
		}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tickCmd(m.cfg.RefreshRate), ctxDoneCmd(m.ctx))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ctxDoneMsg:
		return m, tea.Quit

	case tickMsg:
		if m.cfg.AutoOpen {
			for _, r := range m.fleet.Runners() {
				id := r.Task().ID()
				if r.Status() == agentfleet.StatusRunning && !m.openedTabs[id] {
					m.openedTabs[id] = true
					m.onAttach(id)
				}
			}
		}
		// clamp cursor if tasks disappeared
		active, done := orderedRunners(m.fleet.Runners(), m.cfg.MaxDoneTasks)
		if total := len(active) + len(done); total > 0 && m.cursor >= total {
			m.cursor = total - 1
		}
		return m, tickCmd(m.cfg.RefreshRate)

	case tea.WindowSizeMsg:
		m.termW = msg.Width
		m.termH = msg.Height
		return m, nil

	case tea.KeyMsg:
		active, done := orderedRunners(m.fleet.Runners(), m.cfg.MaxDoneTasks)
		all := append(active, done...)
		total := len(all)
		prevCursor := m.cursor

		switch msg.Type {
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyDown:
			if m.cursor < total-1 {
				m.cursor++
			}
		case tea.KeyEnter:
			if m.cursor < len(all) && all[m.cursor].Status() == agentfleet.StatusRunning {
				m.onAttach(all[m.cursor].Task().ID())
			}
			return m, nil
		case tea.KeyCtrlC:
			return m, tea.Quit
		}

		switch msg.String() {
		case "j":
			if m.cursor < total-1 {
				m.cursor++
			}
		case "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "u":
			m.outScrollBack++
		case "d":
			if m.outScrollBack > 0 {
				m.outScrollBack--
			}
		case "q":
			return m, tea.Quit
		}

		// reset right panel scroll on task change
		if m.cursor != prevCursor {
			m.outScrollBack = 0
		}

		// keep cursor visible in the list (visual row = cursor + 1 if past active section with divider)
		mainH := m.mainHeight()
		visRow := m.cursor
		if m.cursor >= len(active) && len(active) > 0 && len(done) > 0 {
			visRow++ // account for divider row
		}
		if visRow < m.listOffset {
			m.listOffset = visRow
		}
		if visRow >= m.listOffset+mainH {
			m.listOffset = visRow - mainH + 1
		}
	}
	return m, nil
}

// mainHeight returns usable height for the left/right panels.
func (m model) mainHeight() int {
	h := m.termH - 2 // subtract header + footer
	if m.cfg.Log != nil {
		h -= m.termH / 3
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m model) View() string {
	if m.termW == 0 || m.termH == 0 {
		return ""
	}

	active, done := orderedRunners(m.fleet.Runners(), m.cfg.MaxDoneTasks)
	all := append(active, done...)

	mainH := m.mainHeight()
	leftW := m.termW / 2
	rightW := m.termW - leftW

	var selRunner *agentfleet.Runner
	if m.cursor < len(all) {
		selRunner = all[m.cursor]
	}

	header := renderHeader(m, active, done)
	left := renderLeft(m, active, done, mainH, leftW)
	right := renderRight(m, selRunner, mainH, rightW)
	main := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	footer := styleFooter.Render("[↑↓ j/k] navigate  [u/d] scroll output  [enter] attach  [q] quit")

	parts := []string{header, main}
	if m.cfg.Log != nil {
		parts = append(parts, renderLog(m))
	}
	parts = append(parts, footer)
	return strings.Join(parts, "\n")
}

func renderHeader(m model, active, done []*agentfleet.Runner) string {
	var running int
	for _, r := range active {
		if r.Status() == agentfleet.StatusRunning {
			running++
		}
	}
	total := len(active) + len(done)
	summary := fmt.Sprintf("%d tasks · %d running · %d done", total, running, len(done))

	title := styleTitle.Render("◈ agentfleet")
	if m.cfg.Title != nil {
		title = m.cfg.Title()
	}
	return title + "  " + styleSummary.Render(summary)
}

func statusBadge(s agentfleet.Status) string {
	const w = 10
	switch s {
	case agentfleet.StatusRunning:
		return styleRunning.Width(w).Render("● running")
	case agentfleet.StatusDone:
		return styleDone.Width(w).Render("✓ done")
	case agentfleet.StatusFailed:
		return styleFailed.Width(w).Render("✗ failed")
	default:
		return stylePending.Width(w).Render("○ pending")
	}
}

// renderLeft renders the task list left panel.
func renderLeft(m model, active, done []*agentfleet.Runner, mainH, w int) string {
	type row struct {
		runner  *agentfleet.Runner
		idx     int // index into active+done
		divider bool
	}

	var rows []row
	for i, r := range active {
		rows = append(rows, row{runner: r, idx: i})
	}
	if len(active) > 0 && len(done) > 0 {
		rows = append(rows, row{divider: true})
	}
	for i, r := range done {
		rows = append(rows, row{runner: r, idx: len(active) + i})
	}

	offset := m.listOffset
	if offset > len(rows) {
		offset = len(rows)
	}
	visible := rows[offset:]
	if len(visible) > mainH {
		visible = visible[:mainH]
	}

	lines := make([]string, 0, mainH)
	for _, row := range visible {
		if row.divider {
			lines = append(lines, styleDivider.Width(w).Render(strings.Repeat("─", w)))
		} else {
			lines = append(lines, renderRow(row.runner, row.idx == m.cursor, w))
		}
	}
	for len(lines) < mainH {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines, "\n")
}

// renderRow renders a single task row in the left panel.
func renderRow(r *agentfleet.Runner, selected bool, w int) string {
	cursor := "  "
	idStyle := styleMeta
	if selected {
		cursor = "▶ "
		idStyle = styleSelID
	}

	task := r.Task()
	badge := statusBadge(r.Status())

	elapsed := ""
	if r.Status() == agentfleet.StatusRunning {
		d := time.Since(r.StartedAt()).Round(time.Second)
		elapsed = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
	elapsedStr := styleMeta.Width(5).Render(elapsed)

	idStr := idStyle.Render(task.ID())
	cursorW := lipgloss.Width(cursor)
	idW := lipgloss.Width(idStr)
	badgeW := lipgloss.Width(badge)
	elapsedW := lipgloss.Width(elapsedStr)
	nameMaxW := w - cursorW - idW - 2 - badgeW - 1 - elapsedW - 1
	if nameMaxW < 4 {
		nameMaxW = 4
	}
	name := truncateVisual(task.Name(), nameMaxW)

	left := cursor + idStr + "  " + name
	right := badge + " " + elapsedStr
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	rowStr := left + strings.Repeat(" ", gap) + right
	if selected {
		return styleSel.Width(w).Render(rowStr)
	}
	return lipgloss.NewStyle().Width(w).Render(rowStr)
}

// renderRight renders the output panel for the selected task.
func renderRight(m model, r *agentfleet.Runner, mainH, w int) string {
	var rawLines []string
	if r != nil {
		rawLines = r.Lines()
	}

	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = truncateVisual(stripANSI(l), w)
	}

	total := len(lines)
	start := total - mainH - m.outScrollBack
	if start < 0 {
		start = 0
	}
	end := start + mainH
	if end > total {
		end = total
	}

	visible := make([]string, mainH)
	if total > 0 {
		copy(visible, lines[start:end])
	}

	rowStyle := styleOutput.Width(w)
	rendered := make([]string, mainH)
	for i, l := range visible {
		rendered[i] = rowStyle.Render(l)
	}
	return strings.Join(rendered, "\n")
}

// renderLog renders the bottom log panel.
func renderLog(m model) string {
	logH := m.termH / 3
	if logH < 2 {
		logH = 2
	}
	w := m.termW

	allLines := m.cfg.Log.Lines()
	total := len(allLines)
	// show logH-1 lines (1 row is the header)
	contentH := logH - 1
	start := total - contentH
	if start < 0 {
		start = 0
	}

	divider := styleDivider.Width(w).Render(strings.Repeat("─", w))
	rows := []string{divider}
	for _, l := range allLines[start:] {
		rows = append(rows, styleLog.Width(w).Render(truncateVisual(l, w)))
	}
	for len(rows) < logH {
		rows = append(rows, strings.Repeat(" ", w))
	}
	return strings.Join(rows[:logH], "\n")
}

func truncateVisual(s string, maxW int) string {
	w := 0
	runes := []rune(s)
	for i, ch := range runes {
		cw := lipgloss.Width(string(ch))
		if w+cw > maxW {
			if w+1 <= maxW {
				return string(runes[:i]) + "…"
			}
			return string(runes[:i])
		}
		w += cw
	}
	return s
}
```

- [ ] **Step 2: Verify build**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go build ./... 2>&1
```

Expected: clean build (no errors).

- [ ] **Step 3: Run full test suite**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./... 2>&1
```

Expected: all tests pass. Fix any failures before proceeding.

- [ ] **Step 4: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet && git add tui/tui.go && git commit -m "feat: redesign TUI — split-pane layout, always-on elapsed, scrollable list, log panel"
```

---

### Task 5: Verify examples still compile

**Files:**
- Read-only: `examples/`

- [ ] **Step 1: Check examples build**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go build ./examples/... 2>&1
```

If any example references removed fields (`Columns`, `PreviewLines`, `CardWidth`), remove those field assignments from the example files.

- [ ] **Step 2: Run all tests one final time**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./... -count=1 2>&1
```

Expected: all PASS.

- [ ] **Step 3: Tag v0.6.0 and push**

```bash
cd /Users/tan/nweb/opensource/agentfleet && git tag v0.6.0 && git push origin main && git push origin v0.6.0
```

Expected: tag pushed. Verify at `https://github.com/hoaitan/agentfleet/releases/tag/v0.6.0` (or pkg.go.dev after proxy warms up).

---

## Phase 2: retask-cli

### Task 6: Bump agentfleet dependency and wire LogBuffer

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/cmd/sandbox/connect.go`

- [ ] **Step 1: Bump dependency**

```bash
cd /Users/tan/nweb/retask-cli && go get github.com/hoaitan/agentfleet@v0.6.0 && go mod tidy
```

Expected: `go.mod` now shows `github.com/hoaitan/agentfleet v0.6.0`.

- [ ] **Step 2: Update connect.go**

In `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect.go`, make the following changes:

Add `"io"` to the import block (it's needed for `io.Writer`).

Replace the logger setup section (currently lines ~104–107):

```go
			// Logger: non-nil only in headless mode.
			var logger *slog.Logger
			if !useTUI {
				logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
			}
```

With:

```go
			// LogBuffer captures all events; in TUI mode it feeds the log panel,
			// in headless mode it drains to stderr so output is identical.
			logBuf := agentfleet.NewLogBuffer(500)
			var logOut io.Writer = os.Stderr
			if useTUI {
				logOut = logBuf
			}
			logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
```

Then, after `fleetCfg.TUI.Title = makeTitleFunc(...)`, add:

```go
			if useTUI {
				fleetCfg.TUI.Log = logBuf
			}
```

Also add `"io"` and `"log/slog"` to imports if not already present (check the import block at the top of connect.go).

- [ ] **Step 3: Build**

```bash
cd /Users/tan/nweb/retask-cli && go build ./... 2>&1
```

Expected: clean build.

- [ ] **Step 4: Run tests**

```bash
cd /Users/tan/nweb/retask-cli && go test ./... 2>&1
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli && git add go.mod go.sum internal/cmd/sandbox/connect.go && git commit -m "feat: bump agentfleet to v0.6.0 — wire LogBuffer for TUI log panel"
```

---

## Self-Review Checklist

Spec requirement → task coverage:

| Requirement | Task |
|-------------|------|
| LogBuffer (`io.Writer` + `Lines()`) | Task 1 |
| TUIConfig: remove `Columns`/`PreviewLines`/`CardWidth` | Task 2 |
| TUIConfig: add `MaxDoneTasks` (default 10), `Log *LogBuffer` | Task 2 |
| orderedRunners: active newest-first, done capped | Task 3 |
| Scrollable task list with offset | Task 4 (model + renderLeft) |
| Elapsed time always shown on running tasks | Task 4 (renderRow) |
| Split-pane: left 50% task list, right 50% output | Task 4 (View + renderLeft + renderRight) |
| Right panel auto-scroll snap-to-bottom | Task 4 (renderRight + `u`/`d` keys) |
| Log panel bottom 1/3, only when Log != nil | Task 4 (renderLog) |
| Divider between active and done sections | Task 4 (renderLeft) |
| retask-cli: LogBuffer created always | Task 6 |
| retask-cli: TUI mode → log panel; headless → stderr | Task 6 |
| agentfleet v0.6.0 tagged and pushed | Task 5 |
| retask-cli bumped to v0.6.0 | Task 6 |
