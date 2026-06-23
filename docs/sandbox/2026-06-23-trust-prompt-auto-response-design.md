# Design: Reliable trust-prompt auto-response

**Date:** 2026-06-23
**Status:** Approved (pending implementation plan)
**Repos touched:** `retask-cli`, `agentfleet` (`github.com/hoaitan/agentfleet`)

## Problem

When `retask sandbox connect` starts a session that launches Claude Code, the
agent shows a startup trust dialog ("…Yes, I trust this folder"). retask-cli is
supposed to auto-accept it by injecting `Enter`, so unattended sessions don't
stall. In practice the auto-response **does not fire**: the dialog sits there
until a human presses Enter, and only *then* does the `prompt_detected` log
appear.

### Root cause

The current detector (`internal/cmd/sandbox/promptResponder.go`) wraps the PTY
output as an `io.Writer` and scans the **raw byte stream**: it strips ANSI
escapes and lowercases the bytes, then does `strings.Contains(norm, "trust this
folder")`.

Claude Code's Ink TUI draws the dialog with **cursor positioning and
differential, in-place redraws** — it never emits `"…I trust this folder"` as one
contiguous run of bytes. So the substring match never succeeds while the dialog
is on screen. Only when the user presses Enter and Claude tears the dialog down
with a linear repaint does the literal text appear in the byte stream, which is
why detection fires *after* the manual keypress.

agentfleet already runs a proper terminal emulator (`vt10x`, in `vte.go`) per
session and exposes the **rendered screen** via `Runner.Lines()`. The detector
simply isn't using it.

A second, compounding problem: the emulator is created once at
`PTYCols × VTERows` (220 × 200) while the PTY is started at `24 × 220`. The
emulator believes the screen is 200 rows tall; Claude believes it is 24. This
mismatch corrupts the rendered screen and makes even a screen-based match
unreliable. The PTY is also never tracked by the emulator on resize —
`Runner.Resize` moves only the PTY.

## Goals

1. Auto-response reliably accepts the Claude trust dialog without human input.
2. Keep the existing **fire-once** semantics and ~**500ms** cadence ("as what we
   have now") — no aggressive retry loop.
3. The emulator faithfully mirrors the agent's actual screen, at init and after
   every resize.
4. The TUI per-task preview stays capped at ~5 rows regardless of emulator size.

## Non-goals

- No retry/multi-injection strategy. Fire-once.
- No change to the FE/browser live-terminal view (it streams the full session).
- No new flags or env vars for the PTY size (fixed default).

---

## Design

### Part 1 — Detect against the rendered screen (retask-cli only)

Replace the raw-stream `io.Writer` scanner with a **poller over the emulator
screen**.

- Stop wrapping output. `r.SetOutput(...)` goes back to just the `wsWriter`; the
  `promptResponder` `io.Writer` is removed.
- Add a `promptWatcher` launched as a goroutine from `sessionlane.go` when
  `autoRespond` is enabled. It depends only on:
  - a screen provider `func() []string` (production: `r.Lines`),
  - a stdin writer `io.Writer` (production: `r.StdinWriter()`),
  - the rules, a poll interval, and a logger.
- Loop, **every 500ms**:
  1. `screen := normalize(strings.Join(provider(), "\n"))`
  2. For each active rule, if `strings.Contains(screen, rule.match)`:
     - write `rule.send` to stdin **once**,
     - log `prompt_detected` + `prompt_autoresponded`,
     - **deregister the rule (fire-once)**.
- Terminate the goroutine when: all rules have fired, **or** a startup window
  elapses (default 120s — guards a never-shown dialog and avoids a late
  false-positive match), **or** `ctx` / `r.Done()` fires.

Reuse `normalize()`, `rule`, and `defaultPromptRules()` unchanged. Because the
screen provider is an interface, the watcher unit-tests with a fake screen
function and a `bytes.Buffer` stdin — no agentfleet internals required.

**Why fire-once is sufficient now:** the match is tested against the *real
rendered screen*, so a positive match means the interactive menu is fully drawn.
A single `Enter` at that point is accepted. (The old failure was never a dropped
keystroke — it was a match that could never succeed.)

### Part 2 — Emulator tracks the PTY size (agentfleet)

Make `Runner.Lines()` a faithful mirror of what the agent draws.

- `vte.go`: add `func (h *vteHook) Resize(cols, rows int)` — locks `h.mu`, calls
  `h.term.Resize(cols, rows)` (supported via `vt10x.Terminal`'s `View`
  interface), and updates `h.cols` / `h.rows`.
- `runner.go`: `Runner.Resize(rows, cols)` resizes the emulator as well:
  - keep the existing `r.ag.Resize(rows, cols)` (PTY),
  - add `r.vte.Resize(cols, rows)` — **note the argument order**: `pty`/agent
    take `(rows, cols)`; `vt10x` takes `(cols, rows)`.
- `runner.go` `NewRunner`: size the emulator from the **PTY dims**
  (`PTYCols × PTYRows`) instead of `PTYCols × VTERows`. Keep `FleetConfig.VTERows`
  only as a fallback when `PTYRows <= 0`, preserving backward compatibility.

After this, the emulator starts at the PTY size and follows every
`session_resize`, so the rendered screen always matches the agent's view.

### Part 3 — Initial session PTY size 80×90 (retask-cli)

- In `connect.go`, after `fleetCfg := agentfleet.DefaultConfig()`, set the
  session agent dims via named constants:
  - `fleetCfg.Agent.PTYCols = 80`
  - `fleetCfg.Agent.PTYRows = 90`
- The existing `session_resize` handler (`sessionlane.go` `readLoop`) already
  calls `r.Resize(rows, cols)`, which — after Part 2 — moves the emulator with
  the PTY. A 90-row terminal guarantees Claude's full startup render (banner +
  trust dialog) fits on screen for the emulator to capture; 80 is the
  conventional width. Sessions nobody attaches to simply stay at 80×90.

### Part 4 — Preview stays ≤5 rows (no code change; invariant)

`tui/tui.go` already caps the per-task card preview at `previewN = 5` and slices
the **last 5** filtered lines of `r.Lines()`. Card height is `3 + len(preview)`,
so emulator height never changes it. This is recorded as an explicit invariant;
implementation must not raise `previewN`, and an acceptance check confirms the
selected card never exceeds 8 visual rows (3 chrome + 5 preview). The VTE-sync
change improves preview *content* (clean screen instead of garbled mismatch
output) without changing its height.

---

## Data flow (after change)

```
PTY output ──► proxy outChain ──► vteHook (vt10x screen)
                                      ▲
                                      │ Lines()  (every 500ms)
                          promptWatcher ── match? ──► StdinWriter() ──► PTY stdin ("\r")
session_resize ──► Runner.Resize(rows,cols) ──► PTY.Setsize + vteHook.Resize
```

## Components & boundaries

| Unit | Responsibility | Depends on |
|---|---|---|
| `promptWatcher` (retask-cli) | poll screen, match rules, inject once | `func() []string`, `io.Writer`, rules |
| `vteHook.Resize` (agentfleet) | resize the emulator | `vt10x.Terminal.Resize` |
| `Runner.Resize` (agentfleet) | resize PTY **and** emulator together | `PtyAgent.Resize`, `vteHook.Resize` |
| `connect.go` size consts (retask-cli) | set session PTY default 80×90 | `agentfleet.AgentConfig` |

## Testing

- **promptWatcher (unit, retask-cli):** fake screen provider that returns the
  match only after N polls → asserts exactly one injection, rule deregistered,
  and no injection when the match never appears (stops at the window cap).
- **vteHook.Resize / Runner.Resize (unit, agentfleet):** write content, resize,
  assert `Screen()` reflects the new dimensions; assert `Resize` updates both PTY
  and emulator.
- **NewRunner sizing (unit, agentfleet):** emulator initialized to PTY dims;
  `VTERows` fallback honored when `PTYRows <= 0`.
- **Preview invariant (agentfleet):** selected card ≤ 8 visual rows across small
  and large emulator sizes.

## Risks / consequences

- agentfleet's own standalone TUI derives `PTYRows` from the live terminal, so
  its preview emulator will now match the real terminal size instead of a fixed
  200 rows. More faithful, but a behavior change for that public repo — accepted
  (the fork is maintained by us).
- Screen-based polling could, in principle, match a later same-text screen; the
  fire-once + 120s startup window bounds this.

## Acceptance criteria

1. Launching a session with the Claude trust dialog auto-accepts within ~1s of
   the menu rendering, with no human input; `prompt_detected` +
   `prompt_autoresponded` logged before any manual keypress.
2. `session_resize` resizes both the PTY and the emulator; `Runner.Lines()`
   matches the agent's current screen.
3. Session PTY starts at 80×90.
4. Selected TUI task card preview never exceeds 5 rows.
5. `go test ./...` passes in both repos.
