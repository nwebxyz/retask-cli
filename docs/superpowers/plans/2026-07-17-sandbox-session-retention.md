# Sandbox Session Retention & Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Session working folders survive being stopped, are deleted immediately on explicit delete, and are reaped by age otherwise — plus the TUI shows `h:mm:ss` for sessions past an hour.

**Architecture:** `retask sandbox connect` records each session's start time to `<sandbox_id>.json` in its working directory. That log is the only source of truth for what may be deleted. Deletion has two triggers: explicit (`delete_session`, `delete_sandbox`) reclaims disk immediately after draining the PTY, and age (an hourly sweeper, or `retask sandbox cleanup`) reaps folders left behind by stop/disconnect. Stopping — a session, a sandbox, or the CLI itself — never deletes anything.

**Tech Stack:** Go 1.26.4, cobra, testify (`assert`/`require`), `github.com/hoaitan/agentfleet` (TUI + PTY runners), lipgloss.

**Spec:** `docs/superpowers/specs/2026-07-17-sandbox-session-retention-design.md`

## Global Constraints

- Two repos, strictly sequenced: **agentfleet first** (Task 1), then retask-cli (Tasks 2–11). Task 11 bumps the dep and requires a released agentfleet tag, which the user cuts manually.
- `gh` is authenticated as `nwebbot` with **READ only** on `hoaitan/agentfleet` — the agentfleet PR must come from a fork.
- Retention default is `30d`; disabled with `off` only. `--retention 0` is an error.
- `--older-than 0` means "everything". `off` is not valid for `--older-than`.
- Sweep interval is exactly `1 * time.Hour`. Drain timeout is exactly `5 * time.Second`.
- Log file lives at `<baseDir>/<sandbox_id>.json` where `baseDir` is `os.Getwd()` from `connect.go`. Schema version is `1`.
- **Log-only policy:** never delete a `session-*` folder that has no log entry. No mtime fallback, no adoption scan.
- **Stop never deletes.** `Stop`, `StopAll`, and the CLI-stop path must contain no disk access. Deletion lives only in the `delete_session` / `delete_sandbox` branches.
- Named return parameters are required for multi-value returns (repo convention, `CLAUDE.md`).
- Every command's `Long` follows the repo help template: one-line summary, `Usage example:`, `Flags:`.
- Never edit `proto-gen/` by hand.

---

## File Structure

**agentfleet (fork):**
- Modify: `tui/tui.go` — extract `formatElapsed`, widen the elapsed column.
- Create: `tui/tui_test.go` — table test for `formatElapsed`.

**retask-cli:**
- Create: `internal/cmd/sandbox/retention.go` — duration parsing (`parseDuration`, `parseRetention`) and the `retentionSweeper`.
- Create: `internal/cmd/sandbox/retention_test.go`
- Create: `internal/cmd/sandbox/sessionlog.go` — the `sessionLog` store: load/record/remove/sweep/destroy.
- Create: `internal/cmd/sandbox/sessionlog_test.go`
- Create: `internal/cmd/sandbox/cleanup.go` — the `sandbox cleanup` command + log discovery.
- Create: `internal/cmd/sandbox/cleanup_test.go`
- Modify: `internal/cmd/sandbox/sessionlane.go` — `drain`, record-on-start, `Remove` teardown, `RemoveAll`.
- Create: `internal/cmd/sandbox/sessionlane_test.go`
- Modify: `internal/cmd/sandbox/datalane.go:162` — `delete_sandbox` calls `RemoveAll`.
- Modify: `internal/cmd/sandbox/connect.go` — `--retention` flag, log store, sweeper, CLI exit on lane return.
- Modify: `internal/cmd/sandbox/command.go:26` — register `newCleanupCommand`.
- Modify: `internal/cmd/helpcmd/command.go:163` — manifest entries.

Splitting `sessionlog.go` (persistence) from `retention.go` (policy + scheduling) from `cleanup.go` (CLI surface) keeps each file single-purpose; `sessionlane.go` is already 256 lines and only gains teardown logic.

---

## Task 1: agentfleet — `h:mm:ss` elapsed time

**Repo:** `hoaitan/agentfleet` (fork required — `nwebbot` has READ only)

**Files:**
- Modify: `tui/tui.go:550-566` (inside `renderCard`)
- Test: `tui/tui_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `formatElapsed(d time.Duration) string` (package-private to `tui`). Released as a new agentfleet tag consumed by Task 11.

**Context:** `renderCard` currently renders minutes:seconds, so a 90-minute session reads `90:30`. `styleMeta.Width(5)` sizes the column; `1:15:30` needs 7. `renderCard` derives `nameMaxW` from `lipgloss.Width(rightStr)`, so the name column reflows on its own.

- [ ] **Step 1: Fork and clone**

```bash
cd /tmp
gh repo fork hoaitan/agentfleet --clone --remote-name upstream --fork-name agentfleet
cd agentfleet
git checkout -b feat/elapsed-hours
```

Expected: a fork under `nwebbot/agentfleet`, cloned, with `upstream` pointing at `hoaitan/agentfleet`.

- [ ] **Step 2: Write the failing test**

Create `tui/tui_test.go`:

```go
package tui

import (
	"testing"
	"time"
)

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"zero", 0, "00:00"},
		{"seconds", 5 * time.Second, "00:05"},
		{"sub-minute rounds down", 5*time.Second + 400*time.Millisecond, "00:05"},
		{"minutes", 90 * time.Second, "01:30"},
		{"just under an hour", 59*time.Minute + 59*time.Second, "59:59"},
		{"exactly one hour", time.Hour, "1:00:00"},
		{"hours minutes seconds", time.Hour + 15*time.Minute + 30*time.Second, "1:15:30"},
		{"multi-day", 25*time.Hour + time.Minute + 2*time.Second, "25:01:02"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatElapsed(tc.in); got != tc.want {
				t.Errorf("formatElapsed(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

Note: agentfleet's existing tests (`tui/chrome_test.go`) use the stdlib `testing` package, not testify. Match that.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./tui/ -run TestFormatElapsed -v`
Expected: FAIL — `undefined: formatElapsed`

- [ ] **Step 4: Add `formatElapsed`**

In `tui/tui.go`, add above `renderCard`:

```go
// formatElapsed renders a running task's elapsed time. Past one hour it gains
// an hours component, so a long session reads 1:30:00 rather than 90:00.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./tui/ -run TestFormatElapsed -v`
Expected: PASS (all 8 subtests)

- [ ] **Step 6: Call it from `renderCard` and widen the column**

In `tui/tui.go:550-566`, replace:

```go
	elapsed := ""
	if r.Status() == agentfleet.StatusRunning {
		d := time.Since(r.StartedAt()).Round(time.Second)
		elapsed = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
```

with:

```go
	elapsed := ""
	if r.Status() == agentfleet.StatusRunning {
		elapsed = formatElapsed(time.Since(r.StartedAt()))
	}
```

and replace:

```go
	elapsedStr := styleMeta.Width(5).Render(elapsed)
```

with:

```go
	// Width fits "25:01:02"; sub-hour values stay right-sized by padding.
	elapsedStr := styleMeta.Width(8).Render(elapsed)
```

- [ ] **Step 7: Verify the whole package still builds and passes**

Run: `go build ./... && go test ./...`
Expected: PASS, no build errors.

- [ ] **Step 8: Commit and open the PR**

```bash
git add tui/tui.go tui/tui_test.go
git commit -m "feat(tui): show hours in elapsed time past one hour

A 90-minute session rendered as 90:30, which reads as a minute count
rather than an hour and a half. Past 1h the timer now renders h:mm:ss.

The elapsed column widens from 5 to 8 to fit 25:01:02; sub-hour
rendering is unchanged. renderCard derives nameMaxW from the rendered
width, so the name column reflows automatically."
git push -u origin feat/elapsed-hours
gh pr create --repo hoaitan/agentfleet \
  --title "feat(tui): show hours in elapsed time past one hour" \
  --body "Past one hour the task card timer renders \`h:mm:ss\` (\`1:15:30\`) instead of a raw minute count (\`75:30\`).

- Extracts \`formatElapsed\` as a pure function with a table test.
- Widens the elapsed column 5 → 8 to fit \`25:01:02\`. \`renderCard\` derives \`nameMaxW\` from \`lipgloss.Width(rightStr)\`, so the name column reflows automatically.
- Sub-hour rendering is unchanged.

Needed by retask-cli, which shows long-lived sandbox sessions and cannot override the format (\`TUIConfig\` exposes no formatting hook)."
```

**Known limitation (do not fix):** a session running 100+ hours renders 9 characters into a width-8 column, which lipgloss wraps. That requires a continuously-running 4+ day session; accepted rather than complicating the layout.

- [ ] **Step 9: Hand off for release**

Report the PR URL. **STOP** — the user merges and tags the release manually. Tasks 2–10 do not depend on it; only Task 11 does.

---

## Task 2: Duration parsing

**Files:**
- Create: `internal/cmd/sandbox/retention.go`
- Test: `internal/cmd/sandbox/retention_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `parseDuration(s string) (d time.Duration, err error)` — accepts Go duration syntax plus a `d` (days) suffix. Used by `--older-than`.
  - `parseRetention(s string) (d time.Duration, enabled bool, err error)` — as above, plus `off`. Used by `--retention`.

**Context:** `time.ParseDuration` has no day unit, so `30d` fails. Both flags share the grammar but not the keywords: `off` is retention-only, and `0` is meaningful only for `--older-than`.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/sandbox/retention_test.go`:

```go
package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"0d", 0},
		{"12h", 12 * time.Hour},
		{"90m", 90 * time.Minute},
		{"0", 0},
		{" 7d ", 7 * 24 * time.Hour},
	}
	for _, tc := range tests {
		got, err := parseDuration(tc.in)
		require.NoError(t, err, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
}

func TestParseDurationRejects(t *testing.T) {
	for _, in := range []string{"", "off", "30days", "-1d", "-5h", "abc", "d"} {
		_, err := parseDuration(in)
		assert.Error(t, err, "in=%q should be rejected", in)
	}
}

func TestParseRetention(t *testing.T) {
	d, enabled, err := parseRetention("30d")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, 30*24*time.Hour, d)

	for _, in := range []string{"off", "OFF", " off "} {
		_, enabled, err := parseRetention(in)
		require.NoError(t, err, "in=%q", in)
		assert.False(t, enabled, "in=%q should disable retention", in)
	}
}

func TestParseRetentionRejectsZero(t *testing.T) {
	// 0 means "delete everything" for --older-than; allowing it here would
	// make an hourly sweep wipe every folder. Disabling is spelled "off".
	_, _, err := parseRetention("0")
	assert.Error(t, err)
	_, _, err = parseRetention("0d")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestParse' -v`
Expected: FAIL — `undefined: parseDuration`

- [ ] **Step 3: Implement**

Create `internal/cmd/sandbox/retention.go`:

```go
package sandbox

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDuration parses a retention window. It accepts Go duration syntax
// ("12h", "90m", "0") plus a "d" day suffix, which time.ParseDuration rejects.
func parseDuration(s string) (d time.Duration, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration (want e.g. 30d, 12h, 0)")
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, convErr := strconv.ParseFloat(days, 64)
		if convErr != nil {
			return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 12h, 0)", s)
		}
		if n < 0 {
			return 0, fmt.Errorf("duration %q must not be negative", s)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	d, err = time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 12h, 0)", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", s)
	}
	return d, nil
}

// parseRetention parses the --retention flag, which additionally accepts "off".
// Zero is rejected: it means "delete everything" for --older-than, so accepting
// it here would turn an hourly sweep into an hourly wipe. Disabling is "off".
func parseRetention(s string) (d time.Duration, enabled bool, err error) {
	if strings.EqualFold(strings.TrimSpace(s), "off") {
		return 0, false, nil
	}
	d, err = parseDuration(s)
	if err != nil {
		return 0, false, err
	}
	if d == 0 {
		return 0, false, fmt.Errorf(`invalid --retention %q: use "off" to disable retention`, s)
	}
	return d, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestParse' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/retention.go internal/cmd/sandbox/retention_test.go
git commit -m "feat(sandbox): parse retention durations with a day suffix

time.ParseDuration has no day unit, so 30d needs handling. --retention
additionally accepts off; 0 is rejected there because it means delete
everything for --older-than."
```

---

## Task 3: Session log store

**Files:**
- Create: `internal/cmd/sandbox/sessionlog.go`
- Test: `internal/cmd/sandbox/sessionlog_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type sessionEntry struct { Name, Dir string; CreatedAt time.Time }`
  - `newSessionLog(baseDir, sandboxID string) *sessionLog`
  - `sessionLogPath(baseDir, sandboxID string) string`
  - `(*sessionLog) record(sessionID, name, dir string, createdAt time.Time) error`
  - `(*sessionLog) remove(sessionID string) error`
  - `(*sessionLog) entries() (map[string]sessionEntry, error)`
  - `(*sessionLog) destroy() error`
  - `loadSessionLogFile(path string) (*sessionLogData, error)`
  - `errNewerLog` sentinel

**Context:** Concurrent `new_session` events and the hourly sweep both mutate the file, so every mutation takes a mutex and rewrites atomically (temp + rename). Read-modify-write per mutation avoids holding stale in-memory state. `destroy()` latches `closed` so a sweep after `delete_sandbox` cannot recreate the file.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/sandbox/sessionlog_test.go`:

```go
package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionLogRecordAndLoad(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 16, 21, 17, 3, 0, time.UTC)

	require.NoError(t, l.record("sess-a", "My Session", "session-sess-a", now))

	entries, err := l.entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "My Session", entries["sess-a"].Name)
	assert.Equal(t, "session-sess-a", entries["sess-a"].Dir)
	assert.True(t, now.Equal(entries["sess-a"].CreatedAt))

	// The file is named after the sandbox, next to the session folders.
	_, err = os.Stat(filepath.Join(dir, "sb-1.json"))
	assert.NoError(t, err)
}

func TestSessionLogRecordIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Now().UTC()

	require.NoError(t, l.record("sess-a", "First", "session-sess-a", now))
	require.NoError(t, l.record("sess-a", "Second", "session-sess-a", now))

	entries, err := l.entries()
	require.NoError(t, err)
	assert.Len(t, entries, 1, "re-recording a session must not duplicate it")
	assert.Equal(t, "Second", entries["sess-a"].Name)
}

func TestSessionLogRemove(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Now().UTC()
	require.NoError(t, l.record("sess-a", "A", "session-sess-a", now))
	require.NoError(t, l.record("sess-b", "B", "session-sess-b", now))

	require.NoError(t, l.remove("sess-a"))

	entries, err := l.entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	_, ok := entries["sess-b"]
	assert.True(t, ok)
}

func TestSessionLogMissingFileIsEmpty(t *testing.T) {
	l := newSessionLog(t.TempDir(), "sb-nope")
	entries, err := l.entries()
	require.NoError(t, err, "a missing log is the normal first-run case")
	assert.Empty(t, entries)
}

func TestSessionLogIgnoresForeignJSON(t *testing.T) {
	dir := t.TempDir()
	// A cwd can hold ordinary JSON. It must never be read as a session log.
	pkg := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(pkg, []byte(`{"name":"app","version":"1.0.0"}`), 0o644))

	data, err := loadSessionLogFile(pkg)
	require.NoError(t, err)
	assert.Nil(t, data, "package.json must not parse as a session log")
}

func TestSessionLogIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notjson.json")
	require.NoError(t, os.WriteFile(bad, []byte("this is not json"), 0o644))

	data, err := loadSessionLogFile(bad)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestSessionLogRejectsNewerVersion(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sb-future.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"version":99,"sandbox_id":"sb-future","sessions":{}}`), 0o644))

	_, err := loadSessionLogFile(p)
	assert.ErrorIs(t, err, errNewerLog, "an older CLI must not truncate a newer log")
}

func TestSessionLogAtomicWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	require.NoError(t, l.record("sess-a", "A", "session-sess-a", time.Now().UTC()))

	names, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	require.Len(t, names, 1)
	assert.Equal(t, "sb-1.json", filepath.Base(names[0]))
}

func TestSessionLogDestroyDeletesFileAndLatches(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	require.NoError(t, l.record("sess-a", "A", "session-sess-a", time.Now().UTC()))

	require.NoError(t, l.destroy())
	_, err := os.Stat(filepath.Join(dir, "sb-1.json"))
	assert.True(t, os.IsNotExist(err), "destroy must delete the log file")

	// A sweep or a late session start must not resurrect the file.
	require.NoError(t, l.record("sess-b", "B", "session-sess-b", time.Now().UTC()))
	_, err = os.Stat(filepath.Join(dir, "sb-1.json"))
	assert.True(t, os.IsNotExist(err), "a closed log must not be recreated")
}

func TestSessionLogDestroyOnMissingFileIsNoError(t *testing.T) {
	l := newSessionLog(t.TempDir(), "sb-1")
	assert.NoError(t, l.destroy())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSessionLog' -v`
Expected: FAIL — `undefined: newSessionLog`

- [ ] **Step 3: Implement**

Create `internal/cmd/sandbox/sessionlog.go`:

```go
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sessionLogVersion is the schema version written to <sandbox_id>.json.
const sessionLogVersion = 1

// errNewerLog reports a log written by a newer CLI. We skip such files rather
// than rewriting them, so an older binary cannot truncate fields it lost.
var errNewerLog = errors.New("session log written by a newer CLI version")

// sessionEntry is one recorded session.
type sessionEntry struct {
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	CreatedAt time.Time `json:"created_at"`
}

// sessionLogData is the on-disk shape of <sandbox_id>.json.
type sessionLogData struct {
	Version   int                     `json:"version"`
	SandboxID string                  `json:"sandbox_id"`
	Sessions  map[string]sessionEntry `json:"sessions"`
}

// sessionLog owns <baseDir>/<sandboxID>.json. It records when each session
// started so folders can be reaped by age. It is the only source of truth for
// what may be deleted: a session-* folder with no entry is never touched.
//
// Every mutation is a read-modify-write under the mutex, then an atomic
// rewrite, because concurrent new_session events and the retention sweep both
// mutate the file.
type sessionLog struct {
	path      string
	sandboxID string

	mu     sync.Mutex
	closed bool
}

// sessionLogPath returns the log path for a sandbox in baseDir.
func sessionLogPath(baseDir, sandboxID string) string {
	return filepath.Join(baseDir, sandboxID+".json")
}

func newSessionLog(baseDir, sandboxID string) *sessionLog {
	return &sessionLog{path: sessionLogPath(baseDir, sandboxID), sandboxID: sandboxID}
}

// loadSessionLogFile reads a log file. It returns (nil, nil) when the file is
// absent or is not one of ours — a working directory holds ordinary JSON
// (package.json, tsconfig.json) that must never be mistaken for a log.
// It returns errNewerLog for a log from a newer CLI.
func loadSessionLogFile(path string) (data *sessionLogData, err error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d sessionLogData
	if json.Unmarshal(raw, &d) != nil {
		return nil, nil // not JSON we understand — leave it alone
	}
	if d.Version == 0 || d.SandboxID == "" || d.Sessions == nil {
		return nil, nil // valid JSON, but not a session log
	}
	if d.Version > sessionLogVersion {
		return nil, fmt.Errorf("%s: %w (version %d)", path, errNewerLog, d.Version)
	}
	return &d, nil
}

// load returns the current log contents, or a fresh empty one.
// Caller must hold l.mu.
func (l *sessionLog) load() (data *sessionLogData, err error) {
	d, err := loadSessionLogFile(l.path)
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = &sessionLogData{
			Version:   sessionLogVersion,
			SandboxID: l.sandboxID,
			Sessions:  map[string]sessionEntry{},
		}
	}
	return d, nil
}

// save atomically replaces the log file. Caller must hold l.mu.
func (l *sessionLog) save(d *sessionLogData) (err error) {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	// Temp file in the same directory so the rename stays on one filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".sessionlog-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // no-op once renamed

	if _, err = tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, l.path)
}

// record adds or updates a session entry. It is called before bootstrap runs,
// so every folder we create has an entry and stays reapable even if bootstrap
// fails partway.
func (l *sessionLog) record(sessionID, name, dir string, createdAt time.Time) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	d, err := l.load()
	if err != nil {
		return err
	}
	d.Sessions[sessionID] = sessionEntry{Name: name, Dir: dir, CreatedAt: createdAt.UTC()}
	return l.save(d)
}

// remove drops a single session entry.
func (l *sessionLog) remove(sessionID string) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	d, err := l.load()
	if err != nil {
		return err
	}
	if _, ok := d.Sessions[sessionID]; !ok {
		return nil
	}
	delete(d.Sessions, sessionID)
	return l.save(d)
}

// entries returns a copy of the recorded sessions.
func (l *sessionLog) entries() (out map[string]sessionEntry, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d, err := l.load()
	if err != nil {
		return nil, err
	}
	out = make(map[string]sessionEntry, len(d.Sessions))
	for k, v := range d.Sessions {
		out[k] = v
	}
	return out, nil
}

// destroy deletes the log file and closes the store. Closing matters: on
// delete_sandbox the retention sweeper may still be alive, and a sweep tick
// after the file is gone would otherwise recreate a log for a sandbox that no
// longer exists. Once closed, every mutation is a no-op.
func (l *sessionLog) destroy() (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if err = os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSessionLog' -v`
Expected: PASS (10 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/sessionlog.go internal/cmd/sandbox/sessionlog_test.go
git commit -m "feat(sandbox): add per-sandbox session start-time log

Records session start times to <sandbox_id>.json next to the session
folders. The log is the only source of truth for what may be deleted.

Mutations are read-modify-write under a mutex plus an atomic rename,
since session starts and the retention sweep both write it. Files that
aren't ours (package.json) and logs from newer CLIs are left alone."
```

---

## Task 4: Sweep by age

**Files:**
- Modify: `internal/cmd/sandbox/sessionlog.go` (add `sweep`)
- Test: `internal/cmd/sandbox/sessionlog_test.go` (append)

**Interfaces:**
- Consumes: `sessionLog`, `sessionEntry` (Task 3).
- Produces: `(*sessionLog) sweep(baseDir string, now time.Time, olderThan time.Duration, skip func(string) bool, dryRun bool) (deleted []string, err error)` — deletes folders whose entry is at least `olderThan` old, returns sorted session ids. `olderThan == 0` matches everything. `skip` may be nil.

**Context:** One sweep function serves both the hourly goroutine (which passes `sm.isActive` so a long-running session never has its cwd deleted) and the `cleanup` command (which passes nil). `now` is injected so tests never sleep.

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/sessionlog_test.go`:

```go
// mkSession creates a session folder and records it as started at createdAt.
func mkSession(t *testing.T, l *sessionLog, baseDir, id string, createdAt time.Time) string {
	t.Helper()
	dir := "session-" + id
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, dir), 0o755))
	require.NoError(t, l.record(id, id, dir, createdAt))
	return filepath.Join(baseDir, dir)
}

func TestSweepDeletesOldKeepsNew(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	oldDir := mkSession(t, l, dir, "old", now.Add(-40*24*time.Hour))
	newDir := mkSession(t, l, dir, "fresh", now.Add(-2*24*time.Hour))

	deleted, err := l.sweep(dir, now, 30*24*time.Hour, nil, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, deleted)

	_, err = os.Stat(oldDir)
	assert.True(t, os.IsNotExist(err), "aged-out folder should be gone")
	_, err = os.Stat(newDir)
	assert.NoError(t, err, "recent folder must survive")

	entries, err := l.entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	_, ok := entries["fresh"]
	assert.True(t, ok, "sweep must drop the entry with the folder")
}

func TestSweepSkipsLiveSessions(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	// A session running longer than the window must not have its own working
	// directory deleted out from under it.
	liveDir := mkSession(t, l, dir, "live", now.Add(-40*24*time.Hour))

	deleted, err := l.sweep(dir, now, 30*24*time.Hour, func(id string) bool { return id == "live" }, false)
	require.NoError(t, err)
	assert.Empty(t, deleted)

	_, err = os.Stat(liveDir)
	assert.NoError(t, err, "a live session's folder must survive its own age")
}

func TestSweepZeroTakesEverything(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	mkSession(t, l, dir, "a", now.Add(-40*24*time.Hour))
	mkSession(t, l, dir, "b", now) // created this instant

	deleted, err := l.sweep(dir, now, 0, nil, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, deleted, "olderThan 0 means everything")

	entries, err := l.entries()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSweepDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	oldDir := mkSession(t, l, dir, "old", now.Add(-40*24*time.Hour))

	deleted, err := l.sweep(dir, now, 30*24*time.Hour, nil, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, deleted, "dry run still reports what it would delete")

	_, err = os.Stat(oldDir)
	assert.NoError(t, err, "dry run must not delete")

	entries, err := l.entries()
	require.NoError(t, err)
	assert.Len(t, entries, 1, "dry run must not touch the log")
}

func TestSweepIgnoresUnloggedFolders(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	// Log-only policy: a folder with no entry is invisible to cleanup.
	orphan := filepath.Join(dir, "session-orphan")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	mkSession(t, l, dir, "old", now.Add(-40*24*time.Hour))

	deleted, err := l.sweep(dir, now, 30*24*time.Hour, nil, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, deleted)

	_, err = os.Stat(orphan)
	assert.NoError(t, err, "an unlogged folder must never be touched")
}

func TestSweepOnClosedLogIsNoop(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	now := time.Now().UTC()
	mkSession(t, l, dir, "old", now.Add(-40*24*time.Hour))
	require.NoError(t, l.destroy())

	deleted, err := l.sweep(dir, now, 30*24*time.Hour, nil, false)
	require.NoError(t, err)
	assert.Empty(t, deleted, "a closed log must not be swept or recreated")
	_, err = os.Stat(filepath.Join(dir, "sb-1.json"))
	assert.True(t, os.IsNotExist(err))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSweep' -v`
Expected: FAIL — `l.sweep undefined`

- [ ] **Step 3: Implement**

Add to `internal/cmd/sandbox/sessionlog.go` (and add `"sort"` to the imports):

```go
// sweep deletes every logged session at least olderThan old and drops its
// entry, returning the session ids it took. olderThan == 0 matches everything.
//
// skip reports sessions that must not be touched — the retention sweeper
// passes live sessions, so a session running longer than the window never has
// its own working directory deleted underneath it. It may be nil.
//
// Only folders listed in the log are considered: a session-* folder with no
// entry is not ours to delete.
//
// With dryRun, nothing is deleted but the same ids are reported.
func (l *sessionLog) sweep(baseDir string, now time.Time, olderThan time.Duration, skip func(string) bool, dryRun bool) (deleted []string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, nil
	}
	d, err := loadSessionLogFile(l.path)
	if err != nil || d == nil {
		return nil, err
	}

	for id, e := range d.Sessions {
		if skip != nil && skip(id) {
			continue
		}
		if now.Sub(e.CreatedAt) < olderThan {
			continue
		}
		if dryRun {
			deleted = append(deleted, id)
			continue
		}
		if rmErr := os.RemoveAll(filepath.Join(baseDir, e.Dir)); rmErr != nil {
			// Keep the entry so a later sweep retries this folder.
			err = errors.Join(err, rmErr)
			continue
		}
		delete(d.Sessions, id)
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)

	if !dryRun && len(deleted) > 0 {
		if saveErr := l.save(d); saveErr != nil {
			return deleted, errors.Join(err, saveErr)
		}
	}
	return deleted, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSweep' -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/sessionlog.go internal/cmd/sandbox/sessionlog_test.go
git commit -m "feat(sandbox): sweep logged session folders by age

One sweep serves both the hourly goroutine and the cleanup command. The
skip predicate keeps the sweeper from deleting a live session's own
working directory when it outlives the retention window. Folders with no
log entry are never touched."
```

---

## Task 5: Record sessions on start

**Files:**
- Modify: `internal/cmd/sandbox/sessionlane.go:20-61` (SessionManager fields + constructor), `:65-108` (Start)
- Modify: `internal/cmd/sandbox/connect.go:162-171` (construct the log, pass it in)
- Test: `internal/cmd/sandbox/sessionlane_test.go` (create)

**Interfaces:**
- Consumes: `newSessionLog`, `(*sessionLog) record` (Task 3).
- Produces:
  - `SessionManager.log *sessionLog` field.
  - `newSessionManager(..., log *sessionLog, autoRespond bool) *SessionManager` — `log` is added as the **second-to-last** parameter, immediately before `autoRespond`.
  - `(*SessionManager) isActive(sessionID string) bool` — used by the sweeper in Task 8.

**Context:** The entry is recorded **before** `sb.Run` because `SessionBootstrap.setupFolder` creates the folder early (`sessionBootstrap.go:238`) but `Run` can fail later at git clone or config write. Under the log-only policy a folder with no entry can never be cleaned, so recording after success would leak every failed bootstrap permanently.

**Naming note:** `SessionManager` already has a field named `log` (the
`*slog.Logger`). The new field is therefore named `sessionLog`, and every task
below uses `sm.sessionLog`.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/sandbox/sessionlane_test.go`:

```go
package sandbox

import (
	"testing"
	"time"

	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSessionManager builds a SessionManager with only the fields the log
// and teardown tests need — no fleet, no websockets, no PTYs.
func newTestSessionManager(t *testing.T, baseDir string) *SessionManager {
	t.Helper()
	return &SessionManager{
		sandboxID:  "sb-1",
		baseDir:    baseDir,
		sessionLog: newSessionLog(baseDir, "sb-1"),
		sessions:   map[string]*agentfleet.Runner{},
	}
}

func TestRecordSessionStartWritesLogBeforeBootstrap(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)

	start := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	sm.recordSessionStart("sess-a", "My Session", start)

	entries, err := sm.sessionLog.entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "My Session", entries["sess-a"].Name)
	assert.Equal(t, "session-sess-a", entries["sess-a"].Dir)
	assert.True(t, start.Equal(entries["sess-a"].CreatedAt))
}

func TestRecordSessionStartWithNoLogIsSafe(t *testing.T) {
	sm := &SessionManager{baseDir: t.TempDir()}
	assert.NotPanics(t, func() { sm.recordSessionStart("sess-a", "n", time.Now()) })
}

func TestIsActiveTracksLiveSessions(t *testing.T) {
	sm := newTestSessionManager(t, t.TempDir())
	assert.False(t, sm.isActive("sess-a"), "unknown session is not active")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestRecordSessionStart|TestIsActive' -v`
Expected: FAIL — `sm.recordSessionStart undefined`, `unknown field sessionLog`

- [ ] **Step 3: Add the field, constructor param, and helpers**

In `internal/cmd/sandbox/sessionlane.go`, add to the `SessionManager` struct immediately after `endpoint string`:

```go
	sessionLog  *sessionLog // records session start times for retention
```

Update the constructor signature (add `sessionLog` immediately before `autoRespond`):

```go
func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
	workspaceID, sandboxName, baseDir, endpoint string,
	sessionLog *sessionLog,
	autoRespond bool,
) *SessionManager {
```

and inside the returned struct literal add:

```go
		sessionLog:  sessionLog,
```

Add these methods:

```go
// recordSessionStart logs when a session started, before bootstrap runs.
// Bootstrap creates the folder early but can fail afterwards; under the
// log-only retention policy a folder with no entry could never be reaped, so
// the entry must exist as soon as the folder can.
func (sm *SessionManager) recordSessionStart(sessionID, name string, at time.Time) {
	if sm.sessionLog == nil {
		return
	}
	if err := sm.sessionLog.record(sessionID, name, "session-"+sessionID, at); err != nil {
		sm.logError("session_log_record_failed", "session_id", sessionID, "error", err)
	}
}

// isActive reports whether a session currently has a live runner. The
// retention sweeper uses it so a long-running session is never reaped.
func (sm *SessionManager) isActive(sessionID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.sessions[sessionID]
	return ok
}
```

Add `"time"` to the imports.

- [ ] **Step 4: Call it from Start, before bootstrap**

In `sessionlane.go`, in `Start`, immediately before the `sb := &SessionBootstrap{...}` literal (currently line 90), insert:

```go
	// Record before bootstrap: setupFolder creates the folder early but Run can
	// fail later, and an unlogged folder can never be reaped.
	sm.recordSessionStart(sessionID, name, time.Now())
```

- [ ] **Step 5: Update the caller in connect.go**

In `internal/cmd/sandbox/connect.go`, after `baseDir, err := os.Getwd()` (line 157-160) add:

```go
			sessLog := newSessionLog(baseDir, sandboxID)
```

and pass it in the `newSessionManager` call, before `autoRespond`:

```go
			sm := newSessionManager(
				sandboxID, wsBase,
				fleet, fleetCfg.Fleet, fleetCfg.Agent,
				logger,
				sbResp.Msg.WorkspaceId,
				sbResp.Msg.Name,
				baseDir,
				profile.Endpoint,
				sessLog,
				autoRespond,
			)
```

- [ ] **Step 6: Run tests and build**

Run: `go build ./... && go test ./internal/cmd/sandbox/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/sandbox/sessionlane.go internal/cmd/sandbox/sessionlane_test.go internal/cmd/sandbox/connect.go
git commit -m "feat(sandbox): record session start times on session start

The entry is written before bootstrap, not after: setupFolder creates
the folder early but bootstrap can fail later, and under the log-only
retention policy an unlogged folder can never be reaped."
```

---

## Task 6: Drain the PTY before deleting, and delete on `delete_session`

**Files:**
- Modify: `internal/cmd/sandbox/sessionlane.go:203-214` (`Remove`)
- Test: `internal/cmd/sandbox/sessionlane_test.go` (append)

**Interfaces:**
- Consumes: `(*sessionLog) remove` (Task 3), `isActive` (Task 5).
- Produces:
  - `const sessionDrainTimeout = 5 * time.Second`
  - `type stoppableRunner interface { Stop() error; Done() <-chan struct{} }`
  - `drain(r stoppableRunner, timeout time.Duration)`

**Context:** `PtyAgent.Stop` sends SIGTERM and returns immediately — it does not wait for the process. Today's `Remove` deletes the folder right after, racing the agent's own cleanup. SIGTERM exists precisely to grant that grace period. agentfleet never escalates to SIGKILL for a process that *ignores* SIGTERM (its `Kill()` fires only when signal *delivery* fails), so the timeout is the only backstop.

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/sessionlane_test.go`:

```go
// fakeRunner implements stoppableRunner with a controllable exit, so drain
// tests are deterministic instead of timing-dependent.
type fakeRunner struct {
	done     chan struct{}
	stopped  chan struct{}
	exitOnStop bool
}

func newFakeRunner(exitOnStop bool) *fakeRunner {
	return &fakeRunner{
		done:       make(chan struct{}),
		stopped:    make(chan struct{}, 1),
		exitOnStop: exitOnStop,
	}
}

func (f *fakeRunner) Stop() error {
	select {
	case f.stopped <- struct{}{}:
	default:
	}
	if f.exitOnStop {
		close(f.done) // well-behaved agent exits on SIGTERM
	}
	return nil
}

func (f *fakeRunner) Done() <-chan struct{} { return f.done }

func TestDrainWaitsForExit(t *testing.T) {
	f := newFakeRunner(true)

	start := time.Now()
	drain(f, 5*time.Second)

	assert.Less(t, time.Since(start), time.Second, "drain must return as soon as the process exits")
	select {
	case <-f.stopped:
	default:
		t.Fatal("drain must send SIGTERM via Stop")
	}
}

func TestDrainGivesUpAfterTimeout(t *testing.T) {
	// agentfleet never escalates to SIGKILL, so a process that ignores
	// SIGTERM must not block teardown forever.
	f := newFakeRunner(false)

	start := time.Now()
	drain(f, 50*time.Millisecond)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	assert.Less(t, elapsed, time.Second, "drain must give up at the timeout")
}

func TestDrainNilRunnerIsSafe(t *testing.T) {
	assert.NotPanics(t, func() { drain(nil, time.Second) })
}

func TestRemoveDeletesFolderAndLogEntry(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	now := time.Now().UTC()
	sessDir := mkSession(t, sm.sessionLog, dir, "sess-a", now)

	sm.Remove("sess-a")

	_, err := os.Stat(sessDir)
	assert.True(t, os.IsNotExist(err), "delete_session must delete the folder")

	entries, err := sm.sessionLog.entries()
	require.NoError(t, err)
	assert.Empty(t, entries, "delete_session must drop the log entry")
}

func TestStopDoesNotDelete(t *testing.T) {
	// Regression guard: stopping is not deleting. If deletion ever leaks into
	// a stop path, this fails.
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	sessDir := mkSession(t, sm.sessionLog, dir, "sess-a", time.Now().UTC())

	sm.Stop("sess-a")
	sm.StopAll()

	_, err := os.Stat(sessDir)
	assert.NoError(t, err, "Stop/StopAll must never delete a session folder")

	entries, err := sm.sessionLog.entries()
	require.NoError(t, err)
	assert.Len(t, entries, 1, "Stop/StopAll must never touch the log")
}
```

Add `"os"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestDrain|TestRemove|TestStopDoesNot' -v`
Expected: FAIL — `undefined: drain`

- [ ] **Step 3: Implement drain and rewrite Remove**

In `internal/cmd/sandbox/sessionlane.go`, add near the top (after the imports):

```go
// sessionDrainTimeout bounds how long teardown waits for a session's PTY to
// exit after SIGTERM before deleting its folder anyway.
const sessionDrainTimeout = 5 * time.Second

// stoppableRunner is the slice of *agentfleet.Runner that teardown needs, so
// drain can be tested without a real PTY.
type stoppableRunner interface {
	Stop() error
	Done() <-chan struct{}
}

// drain sends SIGTERM and waits for the process to actually exit, up to
// timeout. agentfleet's Stop returns as soon as the signal is delivered, not
// when the process has exited — so deleting a session folder straight after it
// races the agent's own shutdown, destroying the working directory while the
// agent is still flushing into it. SIGTERM exists to grant that grace period.
//
// A process that ignores SIGTERM is never escalated to SIGKILL by agentfleet,
// so the timeout is the only backstop against a hung session blocking teardown.
func drain(r stoppableRunner, timeout time.Duration) {
	if r == nil {
		return
	}
	r.Stop() //nolint:errcheck
	select {
	case <-r.Done():
	case <-time.After(timeout):
	}
}
```

Replace `Remove` (currently lines 203-214) with:

```go
// Remove tears down one session for delete_session: stop the PTY, wait for it
// to exit, then delete its working folder and log entry. An explicit delete
// reclaims disk immediately rather than waiting for the retention window.
func (sm *SessionManager) Remove(sessionID string) {
	sm.logInfo("session_removing", "session_id", sessionID)
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	if r != nil {
		drain(r, sessionDrainTimeout)
		sm.fleet.Remove(sessionID)
	}
	if err := os.RemoveAll(filepath.Join(sm.baseDir, "session-"+sessionID)); err != nil {
		sm.logError("session_dir_remove_failed", "session_id", sessionID, "error", err)
	}
	if sm.sessionLog != nil {
		if err := sm.sessionLog.remove(sessionID); err != nil {
			sm.logError("session_log_remove_failed", "session_id", sessionID, "error", err)
		}
	}
}
```

Note `sm.fleet` is nil in tests, so `Remove` must only touch it when `r != nil` — which the test relies on (it registers no runner).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestDrain|TestRemove|TestStopDoesNot' -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Full package test + build**

Run: `go build ./... && go test ./internal/cmd/sandbox/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/sandbox/sessionlane.go internal/cmd/sandbox/sessionlane_test.go
git commit -m "fix(sandbox): drain the PTY before deleting a session folder

PtyAgent.Stop returns when SIGTERM is delivered, not when the process
exits, so deleting straight after it destroyed the working directory
while the agent was still flushing into it. delete_session now waits for
Runner.Done() (5s cap) before deleting, and drops the log entry too.

agentfleet never escalates to SIGKILL for a process that ignores
SIGTERM, so the timeout is the only backstop.

Adds a regression test that Stop/StopAll never delete."
```

---

## Task 7: `delete_sandbox` full teardown

**Files:**
- Modify: `internal/cmd/sandbox/sessionlane.go` (add `RemoveAll`)
- Modify: `internal/cmd/sandbox/datalane.go:162-167`
- Modify: `internal/cmd/sandbox/connect.go:174`
- Test: `internal/cmd/sandbox/sessionlane_test.go` (append)

**Interfaces:**
- Consumes: `drain`, `sessionDrainTimeout` (Task 6), `(*sessionLog) entries`/`destroy` (Task 3).
- Produces: `(*SessionManager) RemoveAll()` — stop, drain concurrently, delete every logged folder, delete the log file, latch the store closed.

**Context:** `dl.Run(ctx)` is a goroutine (`connect.go:174`), so returning `errSandboxDeleted` currently signals nothing — `ctx` is never cancelled and the TUI keeps running against a sandbox that no longer exists. Cancelling on return reuses the exact path Ctrl-C takes. Deleting the log file while the sweeper is alive would let a later tick recreate it, which `destroy()`'s latch prevents.

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/sessionlane_test.go`:

```go
func TestRemoveAllDeletesEveryFolderAndTheLogFile(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	now := time.Now().UTC()

	a := mkSession(t, sm.sessionLog, dir, "sess-a", now)
	b := mkSession(t, sm.sessionLog, dir, "sess-b", now.Add(-40*24*time.Hour))

	sm.RemoveAll()

	for _, d := range []string{a, b} {
		_, err := os.Stat(d)
		assert.True(t, os.IsNotExist(err), "delete_sandbox must delete every logged folder: %s", d)
	}
	_, err := os.Stat(sessionLogPath(dir, "sb-1"))
	assert.True(t, os.IsNotExist(err), "delete_sandbox must delete the log file")
}

func TestRemoveAllLeavesUnloggedFoldersAlone(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	mkSession(t, sm.sessionLog, dir, "sess-a", time.Now().UTC())

	orphan := filepath.Join(dir, "session-orphan")
	require.NoError(t, os.MkdirAll(orphan, 0o755))

	sm.RemoveAll()

	_, err := os.Stat(orphan)
	assert.NoError(t, err, "log-only policy holds even on sandbox delete")
}

func TestRemoveAllClosesLogAgainstSweeperRace(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	mkSession(t, sm.sessionLog, dir, "sess-a", time.Now().UTC())

	sm.RemoveAll()

	// A sweep tick arriving after teardown must not resurrect the log file.
	_, err := sm.sessionLog.sweep(dir, time.Now(), time.Hour, nil, false)
	require.NoError(t, err)
	_, err = os.Stat(sessionLogPath(dir, "sb-1"))
	assert.True(t, os.IsNotExist(err), "a sweep after teardown must not recreate the log")
}
```

Add `"path/filepath"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestRemoveAll' -v`
Expected: FAIL — `sm.RemoveAll undefined`

- [ ] **Step 3: Implement RemoveAll**

Add to `internal/cmd/sandbox/sessionlane.go` after `Remove`:

```go
// RemoveAll tears everything down for a deleted sandbox: stop and drain every
// live session, delete every folder the log knows about, then delete the log
// file itself. Used for delete_sandbox, after which the CLI exits.
//
// Sessions drain concurrently, so teardown costs one drain timeout rather than
// one per session.
func (sm *SessionManager) RemoveAll() {
	sm.logInfo("sandbox_removing", "sandbox_id", sm.sandboxID)

	sm.mu.Lock()
	runners := make(map[string]*agentfleet.Runner, len(sm.sessions))
	for id, r := range sm.sessions {
		runners[id] = r
	}
	sm.sessions = make(map[string]*agentfleet.Runner)
	sm.mu.Unlock()

	var wg sync.WaitGroup
	for id, r := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			drain(r, sessionDrainTimeout)
			if sm.fleet != nil {
				sm.fleet.Remove(id)
			}
		}()
	}
	wg.Wait()

	if sm.sessionLog == nil {
		return
	}
	// Delete every folder the log knows about — including sessions from earlier
	// runs of this sandbox that are no longer live. Folders with no entry are
	// not ours to touch.
	entries, err := sm.sessionLog.entries()
	if err != nil {
		sm.logError("session_log_read_failed", "sandbox_id", sm.sandboxID, "error", err)
	}
	for id, e := range entries {
		if rmErr := os.RemoveAll(filepath.Join(sm.baseDir, e.Dir)); rmErr != nil {
			sm.logError("session_dir_remove_failed", "session_id", id, "error", rmErr)
		}
	}
	if err := sm.sessionLog.destroy(); err != nil {
		sm.logError("session_log_destroy_failed", "sandbox_id", sm.sandboxID, "error", err)
	}
}
```

`sync` is already imported by this file.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestRemoveAll' -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Wire delete_sandbox to RemoveAll**

In `internal/cmd/sandbox/datalane.go`, replace the `delete_sandbox` case (lines 162-167):

```go
		case "delete_sandbox":
			dl.logInfo("delete_sandbox", "sandbox_id", msg.SandboxID)
			dl.sessions.RemoveAll()
			conn.Close(websocket.StatusNormalClosure, "deleted") //nolint:errcheck
			return errSandboxDeleted
```

- [ ] **Step 6: Exit the CLI when the data lane ends**

In `internal/cmd/sandbox/connect.go`, replace line 174:

```go
			go dl.Run(ctx)
```

with:

```go
			// A deleted sandbox ends the data lane for good; there is nothing
			// left to attach to, so unwind the CLI down the same path a Ctrl-C
			// takes. stop() is idempotent, so returning for any other reason
			// (ctx already cancelled) is harmless.
			go func() {
				dl.Run(ctx)
				stop()
			}()
```

- [ ] **Step 7: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/sandbox/sessionlane.go internal/cmd/sandbox/sessionlane_test.go internal/cmd/sandbox/datalane.go internal/cmd/sandbox/connect.go
git commit -m "feat(sandbox): tear down fully on delete_sandbox

delete_sandbox now stops and drains every session, deletes every logged
folder and the log file, then exits the CLI — previously the data lane
goroutine just returned, leaving a TUI attached to a sandbox that no
longer existed.

Closing the log store matters: the retention sweeper can outlive the
file deletion and would otherwise recreate a log for a dead sandbox."
```

---

## Task 8: Retention sweeper and `--retention`

**Files:**
- Modify: `internal/cmd/sandbox/retention.go` (add `retentionSweeper`)
- Modify: `internal/cmd/sandbox/connect.go` (flag + goroutine + help text)
- Test: `internal/cmd/sandbox/retention_test.go` (append)

**Interfaces:**
- Consumes: `(*sessionLog) sweep` (Task 4), `(*SessionManager) isActive` (Task 5), `parseRetention` (Task 2).
- Produces:
  - `const retentionSweepInterval = time.Hour`
  - `type retentionSweeper struct { log *sessionLog; baseDir string; window, interval time.Duration; isActive func(string) bool; logger *slog.Logger }`
  - `(*retentionSweeper) Run(ctx context.Context)` — sweeps once immediately, then every `interval` until ctx is cancelled.
  - `(*retentionSweeper) once()`

**Context:** Retention is the backstop for folders left by stop/disconnect — explicit deletes reclaim their own disk. Sweeping once at startup means a machine reconnecting after a month cleans up without waiting an hour.

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/retention_test.go`:

```go
func TestSweeperOnceDeletesAgedFolders(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	old := "session-old"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, old), 0o755))
	require.NoError(t, l.record("old", "old", old, time.Now().Add(-40*24*time.Hour)))

	s := &retentionSweeper{log: l, baseDir: dir, window: 30 * 24 * time.Hour}
	s.once()

	_, err := os.Stat(filepath.Join(dir, old))
	assert.True(t, os.IsNotExist(err))
}

func TestSweeperRunSweepsAtStartupThenStops(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	old := "session-old"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, old), 0o755))
	require.NoError(t, l.record("old", "old", old, time.Now().Add(-40*24*time.Hour)))

	// A long interval proves the startup sweep happened, not a tick.
	s := &retentionSweeper{log: l, baseDir: dir, window: 30 * 24 * time.Hour, interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, old))
		return os.IsNotExist(err)
	}, 2*time.Second, 10*time.Millisecond, "sweeper must sweep once at startup")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper must stop when ctx is cancelled")
	}
}
```

Add `"context"`, `"os"`, `"path/filepath"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSweeper' -v`
Expected: FAIL — `undefined: retentionSweeper`

- [ ] **Step 3: Implement the sweeper**

Append to `internal/cmd/sandbox/retention.go` (adding `"context"` and `"log/slog"` to the imports):

```go
// retentionSweepInterval is how often connect re-checks for aged-out folders.
const retentionSweepInterval = time.Hour

// retentionSweeper deletes logged session folders older than window. It is the
// backstop for folders left behind by stop and disconnect — the paths that
// deliberately do not delete. Explicit deletes reclaim their own disk.
type retentionSweeper struct {
	log      *sessionLog
	baseDir  string
	window   time.Duration
	interval time.Duration
	isActive func(string) bool // live sessions are never reaped; may be nil
	logger   *slog.Logger      // may be nil
}

// Run sweeps once immediately, then every interval until ctx is cancelled.
// The startup sweep means a machine reconnecting after a long gap cleans up
// straight away rather than waiting a full interval.
func (s *retentionSweeper) Run(ctx context.Context) {
	s.once()
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.once()
		}
	}
}

func (s *retentionSweeper) once() {
	deleted, err := s.log.sweep(s.baseDir, time.Now(), s.window, s.isActive, false)
	if err != nil && s.logger != nil {
		s.logger.Error("retention_sweep_error", "error", err)
	}
	if len(deleted) > 0 && s.logger != nil {
		s.logger.Info("retention_sweep", "deleted", len(deleted), "session_ids", deleted, "older_than", s.window.String())
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestSweeper' -v`
Expected: PASS

- [ ] **Step 5: Add the flag and start the sweeper**

In `internal/cmd/sandbox/connect.go`, add to the var block at the top of `newConnectCommand`:

```go
	var retention string
```

Register the flag next to the others (after the `--no-auto-respond` registration):

```go
	cmd.Flags().StringVar(&retention, "retention", "30d", `Delete session folders older than this (e.g. 30d, 12h); "off" disables`)
```

In `RunE`, validate early — next to the existing `--mode` validation (line 77-79):

```go
			retentionWindow, retentionOn, err := parseRetention(retention)
			if err != nil {
				return err
			}
```

Note: `err` is already declared later in `RunE` via `cfg, err := config.Load(path)`; because this new statement comes first and uses `:=` with two new variables, it compiles. Verify with the build in Step 7.

After the `sm := newSessionManager(...)` block and before `dl := newDataLane(...)`, add:

```go
			if retentionOn {
				sweeper := &retentionSweeper{
					log:      sessLog,
					baseDir:  baseDir,
					window:   retentionWindow,
					interval: retentionSweepInterval,
					isActive: sm.isActive,
					logger:   logger,
				}
				go sweeper.Run(ctx)
			}
```

Update the command's `Long` to document the flag (repo help template). Replace the `Flags:` block:

```
Flags:
  --mode string      Running mode: auto, tui, headless (default: auto)
  --auto-open        Auto-open a terminal tab for each new session (default: false)
  --no-auto-respond  Disable auto-accepting known agent startup prompts (default: false)
  --retention string Delete session folders older than this, checked hourly. Values: 30d, 12h, off (default: 30d)
```

and add to the usage examples:

```
  retask sandbox connect sandbox_abc123 --retention 7d
  retask sandbox connect sandbox_abc123 --retention off
```

- [ ] **Step 6: Verify the flag rejects bad input**

Append to `internal/cmd/sandbox/retention_test.go`:

```go
func TestConnectRetentionFlagDefault(t *testing.T) {
	cmd := newConnectCommand(&flags.Global{})
	f := cmd.Flags().Lookup("retention")
	require.NotNil(t, f, "--retention must be registered")
	assert.Equal(t, "30d", f.DefValue, "retention defaults to 30 days")
}
```

Add `"github.com/nwebxyz/retask-cli/internal/flags"` to the test imports.

- [ ] **Step 7: Build and test**

Run: `go build ./... && go test ./internal/cmd/sandbox/`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/sandbox/retention.go internal/cmd/sandbox/retention_test.go internal/cmd/sandbox/connect.go
git commit -m "feat(sandbox): sweep aged session folders hourly on connect

--retention 30d (default) deletes logged session folders older than the
window, checked at startup and then hourly; --retention off disables it.

Live sessions are skipped, so a session outliving the window never has
its own working directory deleted underneath it."
```

---

## Task 9: `retask sandbox cleanup`

**Files:**
- Create: `internal/cmd/sandbox/cleanup.go`
- Test: `internal/cmd/sandbox/cleanup_test.go`
- Modify: `internal/cmd/sandbox/command.go:26-35` (register the command)

**Interfaces:**
- Consumes: `parseDuration` (Task 2), `newSessionLog`, `loadSessionLogFile`, `errNewerLog` (Task 3), `(*sessionLog) sweep` (Task 4).
- Produces:
  - `newCleanupCommand(gf *flags.Global) *cobra.Command`
  - `discoverSessionLogs(baseDir string) (logs []*sessionLog, err error)`
  - `confirm(in io.Reader, out io.Writer, prompt string) bool`

**Context:** Bare `cleanup` sweeps every valid log in cwd; an id narrows it. Files failing the schema check are skipped, which is what protects `package.json`. `--older-than 0` prompts, because a separate process cannot know which sessions another process has live.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/sandbox/cleanup_test.go`:

```go
package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverSessionLogsSkipsForeignJSON(t *testing.T) {
	dir := t.TempDir()

	// Two real logs...
	a := newSessionLog(dir, "sb-a")
	require.NoError(t, a.record("s1", "s1", "session-s1", time.Now().UTC()))
	b := newSessionLog(dir, "sb-b")
	require.NoError(t, b.record("s2", "s2", "session-s2", time.Now().UTC()))

	// ...and ordinary files that must be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"app"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{"compilerOptions":{}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`not json`), 0o644))

	logs, err := discoverSessionLogs(dir)
	require.NoError(t, err)

	var ids []string
	for _, l := range logs {
		ids = append(ids, l.sandboxID)
	}
	assert.ElementsMatch(t, []string{"sb-a", "sb-b"}, ids, "only real session logs are discovered")
}

func TestDiscoverSessionLogsEmptyDir(t *testing.T) {
	logs, err := discoverSessionLogs(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, logs)
}

func TestConfirmAcceptsYes(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		var out bytes.Buffer
		assert.True(t, confirm(strings.NewReader(in), &out, "delete? "), "in=%q", in)
	}
}

func TestConfirmRejectsAnythingElse(t *testing.T) {
	for _, in := range []string{"n\n", "\n", "no\n", "maybe\n", ""} {
		var out bytes.Buffer
		assert.False(t, confirm(strings.NewReader(in), &out, "delete? "), "in=%q", in)
	}
}

func TestCleanupDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	sess := filepath.Join(dir, "session-old")
	require.NoError(t, os.MkdirAll(sess, 0o755))
	require.NoError(t, l.record("old", "old", "session-old", time.Now().Add(-40*24*time.Hour)))

	out := runCleanup(t, dir, []string{"--dry-run"})

	_, err := os.Stat(sess)
	assert.NoError(t, err, "--dry-run must not delete")
	assert.Contains(t, out, "old", "dry run reports what it would delete")
}

func TestCleanupDeletesAged(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	old := filepath.Join(dir, "session-old")
	fresh := filepath.Join(dir, "session-fresh")
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	require.NoError(t, l.record("old", "old", "session-old", time.Now().Add(-40*24*time.Hour)))
	require.NoError(t, l.record("fresh", "fresh", "session-fresh", time.Now()))

	runCleanup(t, dir, nil)

	_, err := os.Stat(old)
	assert.True(t, os.IsNotExist(err), "default 30d window reaps a 40-day-old folder")
	_, err = os.Stat(fresh)
	assert.NoError(t, err, "recent folder survives")
}

func TestCleanupOlderThanZeroPromptsAndAborts(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	sess := filepath.Join(dir, "session-a")
	require.NoError(t, os.MkdirAll(sess, 0o755))
	require.NoError(t, l.record("a", "a", "session-a", time.Now()))

	cmd := newCleanupCommand(nil)
	cmd.SetArgs([]string{"--older-than", "0"})
	cmd.SetIn(strings.NewReader("n\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	withWd(t, dir, func() { require.NoError(t, cmd.Execute()) })

	_, err := os.Stat(sess)
	assert.NoError(t, err, "answering n must abort")
	assert.Contains(t, out.String(), "Aborted")
}

func TestCleanupOlderThanZeroWithYesTakesEverything(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	sess := filepath.Join(dir, "session-a")
	require.NoError(t, os.MkdirAll(sess, 0o755))
	require.NoError(t, l.record("a", "a", "session-a", time.Now()))

	runCleanup(t, dir, []string{"--older-than", "0", "--yes"})

	_, err := os.Stat(sess)
	assert.True(t, os.IsNotExist(err), "--older-than 0 --yes deletes everything")
}

func TestCleanupSandboxArgNarrowsScope(t *testing.T) {
	dir := t.TempDir()
	a := newSessionLog(dir, "sb-a")
	b := newSessionLog(dir, "sb-b")
	aDir := filepath.Join(dir, "session-a")
	bDir := filepath.Join(dir, "session-b")
	require.NoError(t, os.MkdirAll(aDir, 0o755))
	require.NoError(t, os.MkdirAll(bDir, 0o755))
	require.NoError(t, a.record("a", "a", "session-a", time.Now().Add(-40*24*time.Hour)))
	require.NoError(t, b.record("b", "b", "session-b", time.Now().Add(-40*24*time.Hour)))

	runCleanup(t, dir, []string{"sb-a"})

	_, err := os.Stat(aDir)
	assert.True(t, os.IsNotExist(err), "named sandbox is swept")
	_, err = os.Stat(bDir)
	assert.NoError(t, err, "other sandboxes are untouched when an id is given")
}

func TestCleanupIgnoresUnloggedFolders(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "session-orphan")
	require.NoError(t, os.MkdirAll(orphan, 0o755))

	runCleanup(t, dir, []string{"--older-than", "0", "--yes"})

	_, err := os.Stat(orphan)
	assert.NoError(t, err, "log-only: a folder with no entry is never deleted")
}

// --- helpers ---

// withWd runs fn with the process working directory set to dir.
func withWd(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(orig)) }()
	fn()
}

// runCleanup executes the cleanup command in dir and returns its output.
func runCleanup(t *testing.T, dir string, args []string) string {
	t.Helper()
	cmd := newCleanupCommand(nil)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	withWd(t, dir, func() { require.NoError(t, cmd.Execute()) })
	return out.String()
}
```

Note: these tests `os.Chdir`, so they must not run in parallel — do not add `t.Parallel()`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run 'TestCleanup|TestDiscover|TestConfirm' -v`
Expected: FAIL — `undefined: newCleanupCommand`

- [ ] **Step 3: Implement**

Create `internal/cmd/sandbox/cleanup.go`:

```go
package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func newCleanupCommand(gf *flags.Global) *cobra.Command {
	var olderThan string
	var dryRun bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "cleanup [sandbox-id]",
		Short: "Delete old session folders in the current directory",
		Long: `Delete session folders left behind by stopped or disconnected sessions.

Only folders recorded in a <sandbox-id>.json session log are considered; any
other directory is left alone. With no argument, every session log in the
current directory is swept.

Usage example:
  retask sandbox cleanup
  retask sandbox cleanup --older-than 7d
  retask sandbox cleanup <sandbox-id> --older-than 7d
  retask sandbox cleanup --older-than 0 --yes
  retask sandbox cleanup --dry-run

Flags:
  --older-than string  Delete folders older than this. Values: 30d, 12h, 0 (0 = everything) (default: 30d)
  --dry-run            Print what would be deleted and exit
  --yes                Skip the confirmation prompt for --older-than 0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			window, err := parseDuration(olderThan)
			if err != nil {
				return err
			}
			baseDir, err := os.Getwd()
			if err != nil {
				return err
			}

			var logs []*sessionLog
			if len(args) == 1 {
				logs = []*sessionLog{newSessionLog(baseDir, args[0])}
			} else if logs, err = discoverSessionLogs(baseDir); err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			// Dry run first so both --dry-run and the prompt report real counts.
			planned := map[*sessionLog][]string{}
			total := 0
			for _, l := range logs {
				ids, sweepErr := l.sweep(baseDir, time.Now(), window, nil, true)
				if sweepErr != nil {
					fmt.Fprintf(out, "skipping %s: %v\n", filepath.Base(l.path), sweepErr)
					continue
				}
				if len(ids) > 0 {
					planned[l] = ids
					total += len(ids)
				}
			}

			if total == 0 {
				fmt.Fprintln(out, "Nothing to clean up.")
				return nil
			}

			for _, l := range logs {
				for _, id := range planned[l] {
					fmt.Fprintf(out, "%s  %s\n", l.sandboxID, id)
				}
			}

			if dryRun {
				fmt.Fprintf(out, "\n%d session folder(s) would be deleted (--dry-run).\n", total)
				return nil
			}

			// A separate process cannot know which sessions are live elsewhere,
			// so wiping everything asks first.
			if window == 0 && !yes {
				prompt := fmt.Sprintf("\nThis will delete %d session folder(s) across %d sandbox(es). Continue? [y/N]: ", total, len(planned))
				if !confirm(cmd.InOrStdin(), out, prompt) {
					fmt.Fprintln(out, "Aborted.")
					return nil
				}
			}

			deletedTotal := 0
			for _, l := range logs {
				if len(planned[l]) == 0 {
					continue
				}
				deleted, sweepErr := l.sweep(baseDir, time.Now(), window, nil, false)
				deletedTotal += len(deleted)
				if sweepErr != nil {
					err = errors.Join(err, sweepErr)
				}
			}
			fmt.Fprintf(out, "\nDeleted %d session folder(s).\n", deletedTotal)
			return err
		},
	}

	cmd.Flags().StringVar(&olderThan, "older-than", "30d", "Delete folders older than this (e.g. 30d, 12h); 0 deletes everything")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be deleted and exit")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt for --older-than 0")
	return cmd
}

// discoverSessionLogs returns every valid session log in baseDir. A working
// directory holds ordinary JSON (package.json, tsconfig.json); anything that
// fails the schema check is skipped, so cleanup can never act on it.
func discoverSessionLogs(baseDir string) (logs []*sessionLog, err error) {
	matches, err := filepath.Glob(filepath.Join(baseDir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	for _, p := range matches {
		d, loadErr := loadSessionLogFile(p)
		if loadErr != nil {
			if errors.Is(loadErr, errNewerLog) {
				continue // written by a newer CLI — not ours to rewrite
			}
			return nil, loadErr
		}
		if d == nil {
			continue // not a session log
		}
		logs = append(logs, newSessionLog(baseDir, d.SandboxID))
	}
	return logs, nil
}

// confirm reads a y/N answer. Anything other than y/yes is a no.
func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
```

- [ ] **Step 4: Register the command**

In `internal/cmd/sandbox/command.go`, add to the `AddCommand` block (after `newAttachCommand(gf)`):

```go
		newCleanupCommand(gf),
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestCleanup|TestDiscover|TestConfirm' -v`
Expected: PASS (10 tests)

- [ ] **Step 6: Try it by hand**

```bash
go build -o /tmp/retask ./cmd/retask/
mkdir -p /tmp/cleanup-demo && cd /tmp/cleanup-demo
mkdir -p session-demo
printf '{\n  "version": 1,\n  "sandbox_id": "sb-demo",\n  "sessions": {\n    "demo": {"name":"demo","dir":"session-demo","created_at":"2020-01-01T00:00:00Z"}\n  }\n}\n' > sb-demo.json
printf '{"name":"app"}' > package.json
/tmp/retask sandbox cleanup --dry-run
```

Expected: reports `sb-demo  demo` and `1 session folder(s) would be deleted (--dry-run).`; `session-demo` and `package.json` both still present.

```bash
/tmp/retask sandbox cleanup
ls
```

Expected: `Deleted 1 session folder(s).`; `session-demo` gone, `package.json` untouched, `sb-demo.json` now has an empty `sessions` map.

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/sandbox/cleanup.go internal/cmd/sandbox/cleanup_test.go internal/cmd/sandbox/command.go
git commit -m "feat(sandbox): add sandbox cleanup command

Sweeps every session log in the working directory (or one named
sandbox). --older-than 0 deletes everything and prompts first, since a
separate process cannot know which sessions are live; --yes skips the
prompt and --dry-run reports without deleting.

Files that fail the log schema check are skipped, so package.json and
friends are never touched."
```

---

## Task 10: `help-llm` manifest

**Files:**
- Modify: `internal/cmd/helpcmd/command.go:163` (connect entry) and add a cleanup entry
- Test: `cmd/retask/main_test.go` (existing sync test — no new test needed)

**Interfaces:**
- Consumes: the command tree from Tasks 8 and 9.
- Produces: nothing consumed by later tasks.

**Context:** `cmd/retask/main_test.go:83` asserts the hand-maintained manifest matches the real command tree in both directions — an undocumented flag or an undocumented command fails the suite. This task exists to satisfy it.

- [ ] **Step 1: Run the sync test to see it fail**

Run: `go test ./cmd/retask/ -run TestHelpLLM -v`
Expected: FAIL — `retask sandbox cleanup` is not documented, and `retask sandbox connect` flags drift (missing `--retention`).

If the test name differs, find it with: `grep -n "func Test" cmd/retask/main_test.go`

- [ ] **Step 2: Update the connect entry**

In `internal/cmd/helpcmd/command.go`, replace line 163:

```go
			{Command: "retask sandbox connect", Description: "Connect this machine as a Private VM sandbox (long-running)", Flags: []string{"--mode", "--auto-open", "--no-auto-respond"}, Example: "retask sandbox connect <sandbox-id>"},
```

with:

```go
			{Command: "retask sandbox connect", Description: "Connect this machine as a Private VM sandbox (long-running). Session folders are created in the current directory and recorded in <sandbox-id>.json. --retention deletes folders older than the window (checked hourly); \"off\" disables it. Live sessions are never deleted", Flags: []string{"--mode", "--auto-open", "--no-auto-respond", "--retention"}, Example: "retask sandbox connect <sandbox-id> --retention 30d"},
```

- [ ] **Step 3: Add the cleanup entry**

Immediately after the `retask sandbox attach` entry (line 164), add:

```go
			{Command: "retask sandbox cleanup", Description: "Delete session folders left by stopped sessions, in the current directory. Only folders recorded in a <sandbox-id>.json session log are considered. With no argument every log in the directory is swept; pass a sandbox id to narrow it. --older-than 0 deletes everything and prompts unless --yes", Flags: []string{"--older-than", "--dry-run", "--yes"}, Example: "retask sandbox cleanup --older-than 7d"},
```

- [ ] **Step 4: Run the sync test to verify it passes**

Run: `go test ./cmd/retask/ -v`
Expected: PASS

- [ ] **Step 5: Eyeball the manifest**

```bash
go build -o /tmp/retask ./cmd/retask/
/tmp/retask help-llm | jq '.commands[] | select(.command | contains("sandbox cleanup") or contains("sandbox connect"))'
```

Expected: both entries present, `--retention` on connect, three flags on cleanup.

- [ ] **Step 6: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/helpcmd/command.go
git commit -m "docs(help-llm): document sandbox cleanup and --retention"
```

---

## Task 11: Bump agentfleet and open the retask-cli PR

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: the agentfleet release from Task 1.
- Produces: the final PR.

**BLOCKED** until the user merges Task 1's PR and pushes a tag. Confirm the released version before starting.

- [ ] **Step 1: Bump the dependency**

```bash
go get github.com/hoaitan/agentfleet@<new-version>
go mod tidy
```

Replace `<new-version>` with the tag the user cut (e.g. `v0.6.28`).

- [ ] **Step 2: Verify the build and suite**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 3: Verify the elapsed format is actually live**

```bash
grep -n "Width(8)" $(go env GOMODCACHE)/github.com/hoaitan/agentfleet@<new-version>/tui/tui.go
```

Expected: the widened column is present — confirming the tag contains Task 1's change rather than an older commit.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): bump agentfleet for h:mm:ss elapsed time

Picks up the TUI change that renders hours in the session panel's
elapsed timer past one hour."
```

- [ ] **Step 5: Push and open the PR**

```bash
git push -u origin feat/sandbox-session-retention
gh pr create --title "feat(sandbox): session folder retention and cleanup" --body "$(cat <<'BODY'
Session working folders now survive being stopped, are deleted immediately on an explicit delete, and are reaped by age otherwise.

## What changed

- **Session log.** `sandbox connect` records each session's start time to `<sandbox_id>.json` beside the session folders. It is the only source of truth for what may be deleted — a `session-*` folder with no entry is never touched. The entry is written *before* bootstrap, since bootstrap creates the folder early but can fail later.
- **Explicit delete reclaims disk now.** `delete_session` deletes the folder and its entry. `delete_sandbox` is a full teardown: stop, drain, delete every logged folder, delete the log file, exit the CLI — previously it left a TUI attached to a sandbox that no longer existed.
- **Drain before delete.** `PtyAgent.Stop` returns when SIGTERM is *delivered*, not when the process exits, so deleting straight after it destroyed the working directory while the agent was still flushing into it. Every delete path now waits on `Runner.Done()` (5s cap) first.
- **Retention.** `--retention 30d` (default, `off` disables) sweeps aged folders at startup and hourly. Live sessions are skipped, so a session outliving the window keeps its own cwd.
- **`retask sandbox cleanup`.** Manual sweep of every log in the working directory, or one named sandbox. `--older-than 0` takes everything (prompts unless `--yes`), `--dry-run` reports only.
- **agentfleet bump** for `h:mm:ss` elapsed time past one hour.

## Notes for reviewers

- **Stop still never deletes** — `Stop`, `StopAll`, and the CLI-stop path contain no disk access, with regression tests pinning that.
- **Log-only, by design.** Folders already on disk before this ships have no log entry and are never auto-reaped; they need a manual `rm`.

Design: `docs/superpowers/specs/2026-07-17-sandbox-session-retention-design.md`
Plan: `docs/superpowers/plans/2026-07-17-sandbox-session-retention.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```

---

## Verification checklist

Run before calling the work done:

- [ ] `go build ./... && go test ./...` passes in retask-cli
- [ ] `go test ./...` passes in the agentfleet fork
- [ ] `retask sandbox cleanup --dry-run` in a folder containing `package.json` leaves it untouched
- [ ] `retask help-llm | jq '.commands[] | select(.command | contains("cleanup"))'` returns the entry
- [ ] `retask sandbox connect <id> --retention off` starts with no sweeper
- [ ] `retask sandbox connect <id> --retention 0` errors, pointing at `off`
