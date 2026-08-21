package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSessionManager builds a SessionManager with only the fields the log
// and teardown tests need — no fleet, no websockets, no PTYs. sessions is left
// empty: these tests cover the "no live entry" teardown path (a restart lost
// it, or nothing was ever attached), matching how Remove/RemoveAll behave when
// sm.sessions has no entry for the id — folder and log cleanup still run.
// drain() itself is exercised directly below via fakeRunner, decoupled from
// SessionManager/sessionEntry construction.
func newTestSessionManager(t *testing.T, baseDir string) *SessionManager {
	t.Helper()
	return &SessionManager{
		sandboxID:  "sb-1",
		baseDir:    baseDir,
		sessionLog: newSessionLog(baseDir, "sb-1"),
		sessions:   map[string]*sessionEntry{},
	}
}

// --- record on start ---

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

func TestActiveCountReflectsLiveSessions(t *testing.T) {
	sm := newTestSessionManager(t, t.TempDir())
	assert.Equal(t, 0, sm.ActiveCount(), "no sessions started yet")

	sm.sessions["sess-a"] = &sessionEntry{}
	assert.Equal(t, 1, sm.ActiveCount())

	sm.sessions["sess-b"] = &sessionEntry{}
	assert.Equal(t, 2, sm.ActiveCount())

	delete(sm.sessions, "sess-a")
	assert.Equal(t, 1, sm.ActiveCount(), "removed session must no longer be counted")
}

// --- drain ---

// fakeRunner implements stoppableRunner with a controllable exit, so drain
// tests are deterministic instead of timing-dependent.
type fakeRunner struct {
	done       chan struct{}
	stopped    chan struct{}
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
		close(f.done) // a well-behaved agent exits on SIGTERM
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
	// agentfleet never escalates to SIGKILL, so a process that ignores SIGTERM
	// must not block teardown forever.
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

// --- delete_session ---

func TestRemoveDeletesFolderAndLogEntry(t *testing.T) {
	dir := t.TempDir()
	sm := newTestSessionManager(t, dir)
	sessDir := mkSession(t, sm.sessionLog, dir, "sess-a", time.Now().UTC())

	sm.Remove("sess-a")

	_, err := os.Stat(sessDir)
	assert.True(t, os.IsNotExist(err), "delete_session must delete the folder")

	entries, err := sm.sessionLog.entries()
	require.NoError(t, err)
	assert.Empty(t, entries, "delete_session must drop the log entry")
}

// --- the stop/delete distinction ---

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

// --- delete_sandbox ---

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
