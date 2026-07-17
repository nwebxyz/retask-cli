# Sandbox session retention & cleanup

Date: 2026-07-17
Status: Approved

## Summary

Session working folders survive being stopped. `retask sandbox connect` records
every session's start time to a per-sandbox log file and sweeps folders older
than a configurable window; a new `retask sandbox cleanup` command exposes the
same sweep for manual use.

Deletion has two triggers, and they are deliberately distinct from stopping:

- **Explicit delete** (`delete_session`, `delete_sandbox`) reclaims disk
  immediately. `delete_sandbox` is a full teardown: it stops sessions, deletes
  their folders and the log file, then exits the CLI.
- **Age** reclaims folders left behind by stop and disconnect.

Stopping a session, stopping a sandbox, and stopping the CLI itself never delete
anything.

Separately, the session panel's elapsed timer gains an hours component.

## Scope

Three deliverables:

1. Elapsed time renders `h:mm:ss` past one hour — **in `agentfleet`, not this repo**.
2. Session folder lifecycle — session log, explicit-delete teardown for
   `delete_session` / `delete_sandbox`, hourly retention sweeper, and a
   `sandbox cleanup` command.
3. `help-llm` manifest updated for the new command and flags.

## 1. Elapsed time (agentfleet)

The panel is rendered by `github.com/hoaitan/agentfleet`, not `retask-cli`.
`tui/tui.go:renderCard` formats elapsed time as minutes:seconds, so a 90-minute
session renders `90:30`:

```go
d := time.Since(r.StartedAt()).Round(time.Second)
elapsed = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
...
elapsedStr := styleMeta.Width(5).Render(elapsed)
```

`agentfleet.TUIConfig` exposes `Title`, `TitleRight`, `AutoOpen`, `Log`,
`OnClose`, and `FilterLines` — no hook for elapsed formatting — and `go.mod`
carries no `replace` directive. The format cannot be overridden from
`retask-cli`. The change must land in agentfleet.

**Decision:** hardcode the format in agentfleet rather than add a config hook.

```go
d := time.Since(r.StartedAt()).Round(time.Second)
if h := int(d.Hours()); h > 0 {
    elapsed = fmt.Sprintf("%d:%02d:%02d", h, int(d.Minutes())%60, int(d.Seconds())%60)
} else {
    elapsed = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
```

`styleMeta.Width(5)` widens to `Width(8)` — `1:15:30` is 7 characters and a
3-digit hour count needs 8. `renderCard` derives `nameMaxW` from
`lipgloss.Width(rightStr)`, so the name column reflows with no further change.

Sub-hour rendering is unchanged (`05:30` stays `05:30`).

**Sequencing:** patch agentfleet → tag a release → bump `go.mod` in `retask-cli`.
Tagging requires credentials with write access to `hoaitan/agentfleet`; the
sandbox's `gh` token has READ only. This item is independently landable and does
not block section 2 or 3.

## 2. Session folder retention

### 2.1 Current behaviour

Contrary to the original premise, stop and disconnect do **not** delete session
folders today. The repo contains exactly one `os.RemoveAll`
(`internal/cmd/sandbox/sessionlane.go:213`), inside `SessionManager.Remove`,
reached only from the `delete_session` data-lane message
(`internal/cmd/sandbox/datalane.go:156`).

| Trigger | Path | Deletes folder? |
|---|---|---|
| `stop_session` | `Stop` → PTY SIGTERM | No |
| `stop_sandbox` | `StopAll` | No |
| Disconnect / TUI exit | `StopAll` | No |
| TUI `x` on a task | `OnClose` → `Stop` + `terminate_session` | No |
| `delete_session` | `Remove` → `os.RemoveAll` | Yes |

So "stop/disconnect must not delete folders" requires no code change. The only
change needed is to `delete_session`.

### 2.2 Session log

New file: `internal/cmd/sandbox/sessionlog.go`.

Location: `<baseDir>/<sandbox_id>.json`, where `baseDir` is `os.Getwd()` from
`connect.go` — the same folder that holds the `session-<id>/` directories it
tracks. Naming by sandbox id means several connected sandboxes can share one
working folder without collision.

```json
{
  "version": 1,
  "sandbox_id": "abf05a5d-5df3-45c2-9944-9dc55e4f8c1f",
  "sessions": {
    "5a868a9c-1320-4146-aa8b-28dae66e33ba": {
      "name": "Tan's MacMini — 2026-07-16 21:17",
      "dir": "session-5a868a9c-1320-4146-aa8b-28dae66e33ba",
      "created_at": "2026-07-16T21:17:03Z"
    }
  }
}
```

```go
type sessionLog struct {
    Version   int                     `json:"version"`
    SandboxID string                  `json:"sandbox_id"`
    Sessions  map[string]sessionEntry `json:"sessions"`
}

type sessionEntry struct {
    Name      string    `json:"name"`
    Dir       string    `json:"dir"`
    CreatedAt time.Time `json:"created_at"`
}
```

Keyed by session id: upsert is idempotent (a reconnect cannot duplicate a row)
and cleanup removal is a map delete.

**Store semantics.** A mutex-guarded store owns the file. Concurrent
`new_session` events and the hourly sweep both mutate it, so every mutation takes
the lock, then rewrites the whole file atomically (write temp in the same
directory, `os.Rename` over the target). At tens of sessions, full rewrite is
cheaper than the complexity of incremental updates.

**Load semantics.** A missing file yields an empty map — not an error; first run
is the common case. A file that parses but lacks `version` / `sandbox_id` /
`sessions` is treated as "not ours" and left untouched. This is what stops a
`cleanup` sweep from touching `package.json` or `tsconfig.json` in a working
folder. A log whose `version` is greater than the version this binary
understands is skipped with a warning rather than rewritten, so an older CLI
cannot silently truncate a newer log's fields.

**Cross-process caveat:** two `connect` processes for the *same* sandbox in the
*same* cwd would race on one log file (last writer wins). Out of scope — that
configuration is already broken for other reasons (both would drive
`session-<id>/` for the same ids). Not defended against.

### 2.3 Write placement

The log entry is recorded in `SessionManager.Start` (`sessionlane.go`)
**immediately before** `sb.Run(ctx, wsConn)` — not after.

`SessionBootstrap.setupFolder` creates the folder early in `Run`, but `Run` can
fail afterward (git clone, agent config write). Under the log-only policy
(§2.4), a folder created without a log entry is invisible to cleanup forever.
Recording before bootstrap guarantees every folder we create is reapable. The
directory path is deterministic (`session-<id>`), so nothing is lost by
recording early.

### 2.4 Orphan folders: out of scope, by decision

`session-*` folders with no log entry are ignored entirely. Cleanup only ever
deletes what the log lists.

**Accepted consequence:** folders on disk before this feature ships, and any
folder created while the log was missing or deleted, are never auto-reaped and
must be removed by hand. An adoption scan (recording unclaimed folders using
filesystem mtime) was considered and explicitly rejected.

### 2.5 Deletion policy

Deleting is explicit and immediate; stopping never deletes. The two must not be
conflated — the whole point of the split is that a user can stop work and come
back to their files.

| Trigger | Session PTYs | Folders | `<sandbox_id>.json` | CLI |
|---|---|---|---|---|
| User stops CLI (Ctrl-C / `kill`) | SIGTERM | keep | keep | exits |
| `stop_session` | SIGTERM | keep | keep | runs on |
| `stop_sandbox` | SIGTERM (all) | keep | keep | runs on |
| Retention sweep / `cleanup` | untouched (live skipped) | delete aged | drop entries | runs on |
| `delete_session` | SIGTERM | delete one | drop that entry | runs on |
| `delete_sandbox` | SIGTERM (all) | delete all | delete file | **exits** |

This is enforced structurally: `Stop` and `StopAll` stay pure "signal the
process" operations containing no disk access, and deletion lives only in the
`delete_session` / `delete_sandbox` branches of the data lane. The CLI-stop path
(`connect.go:193`) calls `StopAll` and returns, so it cannot acquire deletion
behaviour by accident.

**The drain wait.** `PtyAgent.Stop` sends SIGTERM and returns immediately —
it does not wait for the process to exit:

```go
if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
    return a.cmd.Process.Kill()   // only if delivery failed, i.e. already gone
}
return nil
```

So today's `Remove` already races: it deletes the session folder while the agent
is still handling SIGTERM and may be flushing files into it. SIGTERM's entire
purpose is to grant that grace period, so deleting the directory mid-cleanup
defeats it. Every delete path therefore stops the PTY, waits on `Runner.Done()`
(closed at `runner.go:159` on process exit) up to a bounded timeout, and only
then deletes:

```go
r.Stop()                                  // SIGTERM
select {
case <-r.Done():                          // clean exit
case <-time.After(sessionDrainTimeout):   // hung; reap anyway
}
os.RemoveAll(dir)
```

A hung process still gets its folder reclaimed after the timeout, so a stuck
agent cannot block teardown indefinitely. Note that agentfleet never escalates
to `SIGKILL` for a process that *ignores* SIGTERM (the `Kill()` above fires only
when delivery fails), so the timeout is the only backstop. `delete_sandbox`
drains sessions concurrently, bounding total teardown at one timeout rather than
one per session.

`sessionDrainTimeout` is 5 seconds.

### 2.6 `delete_sandbox` teardown

`delete_sandbox` tears the whole thing down and exits the CLI — leaving a TUI
attached to a sandbox that no longer exists is not a useful state.

Sequence, in `SessionManager`:

1. `StopAll` — SIGTERM every live session.
2. Drain — wait on each `Runner.Done()` concurrently, bounded by
   `sessionDrainTimeout`.
3. Delete every session folder listed in the log.
4. Delete `<sandbox_id>.json`.
5. Close the log store (§2.6.1).
6. Return `errSandboxDeleted`, which unwinds `DataLane.Run`.

Then the CLI exits. `dl.Run(ctx)` is launched as a goroutine
(`connect.go:174`), so its return currently signals nothing — `ctx` is never
cancelled and the TUI keeps running. The fix is to cancel on return:

```go
go func() {
    dl.Run(ctx)
    stop()   // cancel ctx: unblocks tui.Run / <-ctx.Done(), then StopAll
}()
```

`stop()` is the existing `signal.NotifyContext` cancel from `connect.go:94`, so
this reuses the exact path a Ctrl-C already takes — `tui.Run` returns, the
deferred `stop()` is a no-op (cancel is idempotent), and `sm.StopAll()` at
`connect.go:193` runs against an already-empty session map. Calling `stop()`
when `Run` returns for any other reason (ctx already cancelled) is equally
harmless.

#### 2.6.1 Closing the log store

Deleting `<sandbox_id>.json` while the retention sweeper is still alive is a
resurrection hazard: a sweep tick between step 4 and the TUI actually exiting
would rewrite the file we just deleted, leaving a log for a sandbox that no
longer exists.

Cancelling `ctx` first would stop the sweeper but kill the TUI before teardown
finishes, so ordering alone cannot fix it. Instead the store gets an explicit
`Close()` that latches a `closed` flag under the same mutex that guards writes;
every subsequent mutation becomes a no-op. Teardown is then correct regardless
of how the sweeper and the exit path interleave.

### 2.7 Retention sweeper

Retention is the backstop for folders left behind by stop and disconnect —
the paths that deliberately do not delete. Explicit deletes reclaim their own
disk immediately (§2.5), so the sweeper exists for the folders nobody ever
explicitly deleted.

`retask sandbox connect` gains `--retention` (default `30d`; `off` disables).
When enabled, a goroutine sweeps once at startup and then hourly until the
command's context is cancelled.

Each sweep, for every entry older than the window: delete the folder, drop the
entry, rewrite the log.

**Live-session guard.** The sweeper skips any session id currently in
`SessionManager.sessions`. Without it, a session running longer than the
retention window would have its own working directory deleted out from under its
PTY.

`--retention 30d` is not valid `time.ParseDuration` input (no `d` unit), so a
`parseDuration` helper handles the `d` suffix on top of the standard units.

```go
// parseDuration accepts "30d", "12h", "0". Shared by both flags.
func parseDuration(s string) (d time.Duration, err error)

// parseRetention wraps it, additionally accepting "off".
func parseRetention(s string) (d time.Duration, enabled bool, err error)
```

The two flags share the duration grammar but not their keywords: `off` is valid
only for `--retention` (it is an error for `--older-than`), and `--retention 0`
is an error rather than a silent "reap everything hourly" — disabling is spelled
`off` and only `off`.

## 3. `retask sandbox cleanup`

New file: `internal/cmd/sandbox/cleanup.go`, wired into the `sandbox` command's
`AddCommand` block.

```
retask sandbox cleanup                        # every log in cwd, default 30d
retask sandbox cleanup <sandbox-id>           # one sandbox
retask sandbox cleanup --older-than 7d
retask sandbox cleanup --older-than 0         # everything (prompts)
retask sandbox cleanup --older-than 0 --yes   # everything, no prompt
retask sandbox cleanup --dry-run              # report only
```

**Flags:** `--older-than` (default `30d`), `--dry-run`, `--yes`.

**Vocabulary.** `--retention 30d` and `--older-than 30d` share one duration
grammar. Retention disables via `off` rather than `0`, which frees `0` to
unambiguously mean "delete everything" — the two meanings cannot collide.

**Scope.** With no argument, every valid log in cwd (files failing the §2.2
schema check are skipped). With an argument, only `<sandbox-id>.json`.

**Confirmation.** `--older-than 0` prompts before deleting, because a separate
process cannot know which sessions are live. `--yes` bypasses for scripts.
Non-zero windows do not prompt.

**Shared implementation.** The command and the sweeper call one sweep function.
The sweeper passes its live-session set; the command passes an empty set.

## 4. `help-llm`

`cmd/retask/main_test.go:83` asserts the hand-maintained manifest matches the
command tree — an undocumented flag fails the build. Update
`internal/cmd/helpcmd/command.go`:

- Add `retask sandbox cleanup` with `--older-than`, `--dry-run`, `--yes`.
- Add `--retention` to the existing `retask sandbox connect` entry (line 163).

Also update the `Long` help on `connect` (which documents its flags inline) per
the repo's help-text template.

## 5. Testing

| File | Cases |
|---|---|
| `sessionlog_test.go` | round-trip save/load; upsert idempotency; atomic write leaves no temp file; missing file → empty map; foreign JSON (`package.json`) rejected |
| `sessionlog_test.go` | `parseRetention`: `30d`, `12h`, `off`, invalid input |
| `sessionlog_test.go` | sweep: reaps older-than-window, keeps newer, removes entry + folder together, skips live sessions |
| `cleanup_test.go` | multi-log cwd; foreign JSON skipped; `--dry-run` deletes nothing; `--older-than 0` takes all; single-sandbox arg narrows scope |
| `sessionlane_test.go` | `delete_session` deletes folder + drops entry; `delete_sandbox` deletes all folders + the log file; drain waits for `Done()` before deleting; drain gives up after the timeout on a process that ignores SIGTERM |
| `sessionlane_test.go` | **stop does not delete**: `Stop`, `StopAll`, and the CLI-stop path leave folders and log intact |
| `sessionlog_test.go` | `Close()` latches: a sweep after teardown cannot recreate the deleted log file |
| `main_test.go` | existing manifest sync test covers the new command and flags |

Sweep tests inject a clock and base directory rather than sleeping. Drain tests
use a fake runner exposing a controllable `Done()` channel, so the SIGTERM-race
and timeout cases are deterministic rather than timing-dependent.

The "stop does not delete" cases are the regression guard for the distinction in
§2.5 — they fail loudly if deletion ever leaks into a stop path.

## Decisions rejected

- **`FormatElapsed` hook in agentfleet's `TUIConfig`** — more flexible, but a
  larger API change than the format warrants.
- **Adopting orphan folders via mtime** — would have reaped the pre-existing
  backlog; rejected in favour of a strict log-only policy.
- **Deferring `delete_session` folder removal to the sweeper** — briefly adopted,
  then reversed: an explicit delete should reclaim its disk immediately rather
  than leave the folder sitting for up to the retention window.
- **Deleting folders without draining the PTY** — matches today's behaviour, but
  destroys the working directory while the agent is still handling SIGTERM.
- **`--after-days 30` (numeric)** — closer to the original phrasing, but splits
  the duration vocabulary and cannot express sub-day windows.
