# Design: sandbox connect TUI redesign + agentfleet v0.6.0

**Date:** 2026-06-15  
**Repos:** `github.com/hoaitan/agentfleet` (v0.6.0), `github.com/nwebxyz/retask-cli` (current PR `feat/private-sandbox-connect`)

---

## Motivation

The current `retask sandbox connect` TUI shows a card-per-task list with a 3-line output preview on the selected card only. For AI agent workloads this is low-signal: the output is often rich conversation text that needs more space, and the layout wastes screen real estate on card borders. The elapsed time is hidden behind selection, finished tasks pile up, and there is no visibility into retask CLI connection/session events without switching to headless mode.

---

## agentfleet v0.6.0 changes

### 1. TUIConfig API

Remove `Columns`, `PreviewLines`, `CardWidth` (unused after this redesign). Add:

```go
type TUIConfig struct {
    Title        func() string // header title func; nil = "◈ agentfleet"
    RefreshRate  time.Duration // default: 500ms
    MaxDoneTasks int           // done/failed tasks kept in list; 0 = no limit; default: 10
    Log          *LogBuffer    // nil = no log panel
    AutoOpen     bool          // auto-open tab when task starts; default: true
}
```

`DefaultConfig()` sets `MaxDoneTasks: 10`, `Log: nil`, `AutoOpen: true`.

### 2. LogBuffer type

New exported type in the `agentfleet` package:

```go
type LogBuffer struct { /* ring buffer, thread-safe */ }

func NewLogBuffer(maxLines int) *LogBuffer
func (b *LogBuffer) Write(p []byte) (int, error)  // implements io.Writer
func (b *LogBuffer) Lines() []string               // snapshot (newest last)
```

Thread-safe. `Write` splits on newlines and appends to the ring. `Lines` returns a copy of current contents. Used by both the TUI renderer and (indirectly) by callers that pass it to `slog.NewTextHandler`.

### 3. TUI layout

```
┌ title / connection status ───────────────────────────────────────────┐
│  Left (termW/2)                 Right (termW/2)                      │
│  ▶ abc12  agent-1  ● 03:22      [output line 1]                      │
│    def34  agent-2  ● 01:05      [output line 2]                      │
│    ────────────────────────     [output line 3]                      │
│    ghi56  old-task  ✓            [output line 4]                      │
│                                  [output line 5]                      │
├ Logs (termH/3, only if Log != nil) ──────────────────────────────────┤
│  08:14:20 INF connected sandbox=sandbox_abc                          │
│  08:14:22 INF session started session=abc12                          │
└──────────────────────────────────────────────────────────────────────┘
[↑↓ j/k] navigate  [enter] attach  [q] quit
```

**Left panel (task list):**
- One row per task: `[cursor] [id]  [name truncated]  [status badge] [elapsed?]`
- Elapsed shown always for running tasks (e.g. `03:22`), blank for done/pending
- Ordering: active tasks (running + pending) newest-first; a divider line separates them from done/failed tasks (oldest-first, capped at `MaxDoneTasks`)
- Scrollable: model tracks `scrollOffset`; cursor movement keeps selected row in view
- Width: `termW / 2`

**Right panel (output):**
- Displays `runner.Lines()` ring buffer of the currently selected task
- Stripped of ANSI escape sequences
- Auto-scrolls to bottom as new lines arrive; if the user has scrolled up, stays at their position until they scroll back to bottom ("snap-back")
- Right panel scroll state is per-task (reset when cursor moves to a new task)
- Width: `termW / 2`; height: main area height (full or 2/3 depending on log panel)

**Log panel (bottom):**
- Present only when `TUIConfig.Log != nil`
- Height: `termH / 3`
- Shows `LogBuffer.Lines()` newest-last, auto-scrolls to bottom (no manual scroll needed)
- Styled differently from task output (dimmer color)

**Main area height:**
- With log panel: `termH - 1 (header) - 1 (footer) - termH/3 (log)`
- Without log panel: `termH - 1 (header) - 1 (footer)`

### 4. model struct additions

```go
type model struct {
    // existing
    fleet      *agentfleet.Fleet
    cfg        agentfleet.TUIConfig
    onAttach   func(taskID string)
    ctx        context.Context
    cursor     int
    termW      int
    termH      int
    openedTabs map[string]bool

    // new
    listOffset   int            // scroll offset for task list
    outOffset    int            // scroll offset for right output panel
    outAtBottom  bool           // whether right panel is snapped to bottom
    outTaskID    string         // which task the outOffset belongs to (reset on cursor change)
}
```

### 5. Behavior: finished tasks

When a runner reaches `StatusDone` or `StatusFailed`:
- It moves to the bottom section of the list
- The bottom section keeps at most `MaxDoneTasks` entries (oldest dropped when over limit; 0 = no limit)
- The task's Unix socket closes naturally, causing any `sandbox attach` process to exit

---

## retask-cli changes

### connect.go

```go
// Always create logBuf — feeds TUI log panel or stderr
logBuf := agentfleet.NewLogBuffer(500)

var logOut io.Writer = os.Stderr
if useTUI {
    logOut = logBuf
}
logger = slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))

// Wire log panel only in TUI mode
if useTUI {
    fleetCfg.TUI.Log = logBuf
}
```

The `sessionManager` and `dataLane` already accept `*slog.Logger` — no other changes needed. The logger now always emits events (connected, session start/stop, errors) in both modes; in TUI mode they surface in the log panel instead of stderr.

### Dependency bump

After agentfleet v0.6.0 is tagged and pushed:

```bash
go get github.com/hoaitan/agentfleet@v0.6.0
go mod tidy
```

---

## Release sequence

1. Implement all agentfleet changes, run tests, push to `main`
2. Tag `v0.6.0` on agentfleet
3. Bump dependency in retask-cli (`feat/private-sandbox-connect` branch)
4. Test `retask sandbox connect` end-to-end

---

## Out of scope

- Full-screen PTY overlay (option B from brainstorm) — deferred
- Inline PTY rendering in right panel (option C) — deferred
- Log panel manual scroll — auto-scroll to bottom is sufficient
