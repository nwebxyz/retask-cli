package sandbox

import (
	"sync"

	"github.com/coder/websocket"
	agentfleet "github.com/hoaitan/agentfleet"
)

// sessionEntry tracks one live agent runner and its current session-lane
// connection. The connection changes over the session's life: it is nilled on
// detach (a transport drop) and swapped on re-attach (a fresh new_session for
// an already-running PTY). The runner OUTLIVES any individual connection — it
// is stopped only by an explicit Stop/Remove, never by a dropped socket.
type sessionEntry struct {
	runner *agentfleet.Runner

	mu     sync.Mutex
	conn   *websocket.Conn // current session-lane conn; nil when detached
	reaped bool            // true once the PTY has exited; blocks further swapConn
}

func newSessionEntry(r *agentfleet.Runner, conn *websocket.Conn) *sessionEntry {
	return &sessionEntry{runner: r, conn: conn}
}

// currentConn returns the active session-lane conn, or nil when detached.
func (e *sessionEntry) currentConn() *websocket.Conn {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn
}

// swapConn installs conn as the current session-lane and returns the previous
// one (which the caller should close). onSwap, if non-nil, runs while the entry
// lock is held — use it to repoint the runner's output atomically with the conn
// change. It returns ok=false and does nothing if the entry has been reaped (its
// PTY exited); the caller must not bind a re-attach onto a dead runner.
func (e *sessionEntry) swapConn(conn *websocket.Conn, onSwap func()) (old *websocket.Conn, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reaped {
		return nil, false
	}
	old = e.conn
	e.conn = conn
	if onSwap != nil {
		onSwap()
	}
	return old, true
}

// reap marks the entry as reaped (its PTY has exited) and returns the current
// conn for the caller to close, clearing it. After reap, swapConn refuses, so a
// re-attach racing the delete can't bind onto the dead runner.
func (e *sessionEntry) reap() (conn *websocket.Conn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reaped = true
	conn = e.conn
	e.conn = nil
	return conn
}

// detachIfCurrent clears the connection only if it is still conn, returning true
// when it did. onDetach, if non-nil, runs (only on a true match) while the entry
// lock is held — use it to redirect the runner's output atomically with the
// detach, so it never clobbers a concurrent re-attach's output.
func (e *sessionEntry) detachIfCurrent(conn *websocket.Conn, onDetach func()) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn != conn {
		return false
	}
	e.conn = nil
	if onDetach != nil {
		onDetach()
	}
	return true
}
