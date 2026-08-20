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
	_, err = os.Stat(filepath.Join(dir, "sandbox_sb-1.json"))
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
	// A working directory holds ordinary JSON. It must never read as a log.
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
	assert.Equal(t, "sandbox_sb-1.json", filepath.Base(names[0]))
}

func TestSessionLogDestroyDeletesFileAndLatches(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	require.NoError(t, l.record("sess-a", "A", "session-sess-a", time.Now().UTC()))

	require.NoError(t, l.destroy())
	_, err := os.Stat(filepath.Join(dir, "sandbox_sb-1.json"))
	assert.True(t, os.IsNotExist(err), "destroy must delete the log file")

	// A sweep or a late session start must not resurrect the file.
	require.NoError(t, l.record("sess-b", "B", "session-sess-b", time.Now().UTC()))
	_, err = os.Stat(filepath.Join(dir, "sandbox_sb-1.json"))
	assert.True(t, os.IsNotExist(err), "a closed log must not be recreated")
}

func TestSessionLogDestroyOnMissingFileIsNoError(t *testing.T) {
	l := newSessionLog(t.TempDir(), "sb-1")
	assert.NoError(t, l.destroy())
}

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
	_, err = os.Stat(filepath.Join(dir, "sandbox_sb-1.json"))
	assert.True(t, os.IsNotExist(err))
}
