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

func TestCleanupNothingToDo(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "session-fresh"), 0o755))
	require.NoError(t, l.record("fresh", "fresh", "session-fresh", time.Now()))

	out := runCleanup(t, dir, nil)
	assert.Contains(t, out, "Nothing to clean up.")
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

func TestCleanupRejectsBadOlderThan(t *testing.T) {
	dir := t.TempDir()
	cmd := newCleanupCommand(nil)
	cmd.SetArgs([]string{"--older-than", "off"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	withWd(t, dir, func() {
		assert.Error(t, cmd.Execute(), `"off" is retention-only, not valid for --older-than`)
	})
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
