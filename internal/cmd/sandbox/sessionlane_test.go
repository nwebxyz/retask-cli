package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRemoveTestManager builds a minimal manager for exercising Remove()'s
// folder cleanup: no fleet/runner needed since the session id under test has
// no live entry.
func newRemoveTestManager(baseDir string, keepSessionFolder bool) *SessionManager {
	return &SessionManager{
		baseDir:           baseDir,
		keepSessionFolder: keepSessionFolder,
		sessions:          make(map[string]*sessionEntry),
		creating:          make(map[string]struct{}),
	}
}

func TestRemove_DeletesSessionFolderByDefault(t *testing.T) {
	baseDir := t.TempDir()
	sessionID := "sess-delete"
	sessionDir := filepath.Join(baseDir, "session-"+sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sm := newRemoveTestManager(baseDir, false)
	sm.Remove(sessionID)

	_, err := os.Stat(sessionDir)
	assert.True(t, os.IsNotExist(err), "expected session folder to be deleted, stat err=%v", err)
}

func TestRemove_KeepsSessionFolderWhenFlagSet(t *testing.T) {
	baseDir := t.TempDir()
	sessionID := "sess-keep"
	sessionDir := filepath.Join(baseDir, "session-"+sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sm := newRemoveTestManager(baseDir, true)
	sm.Remove(sessionID)

	_, err := os.Stat(sessionDir)
	assert.NoError(t, err, "expected session folder to be kept")
}
