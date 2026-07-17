// internal/cmd/sandbox/sessionlog.go
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// sessionLogVersion is the schema version written to <sandbox_id>.json.
const sessionLogVersion = 1

// errNewerLog reports a log written by a newer CLI. Such files are skipped
// rather than rewritten, so an older binary cannot truncate fields it lost.
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
// Every mutation is a read-modify-write under the mutex followed by an atomic
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
// (package.json, tsconfig.json) that must never be mistaken for a log. It
// returns errNewerLog for a log written by a newer CLI.
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

	// Temp file in the same directory keeps the rename on one filesystem.
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

// sweep deletes every logged session at least olderThan old and drops its
// entry, returning the session ids it took. olderThan == 0 matches everything.
//
// skip reports sessions that must not be touched — the retention sweeper passes
// live sessions, so a session running longer than the window never has its own
// working directory deleted underneath it. It may be nil.
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
