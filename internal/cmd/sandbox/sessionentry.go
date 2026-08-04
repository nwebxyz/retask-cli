package sandbox

import (
	"sync"

	"github.com/coder/websocket"
	agentfleet "github.com/hoaitan/agentfleet"
)

// sessionEntry tracks one live agent runner and its current session-lane
// connection. The connection changes over the session's life: it is nilled on
// detach (a transport drop) and swapped on re-attach (a reconnect — CLI-driven
// or a fresh new_session for an already-running PTY). The runner OUTLIVES any
// individual connection — it is stopped only by an explicit Stop/Remove, never
// by a dropped socket.
//
// Output is decoupled from the connection: the runner writes to a permanent
// sessionWriter (set once at create), which sends to the current conn when
// attached and buffers (drop-oldest) while detached, flushing the backlog on
// re-attach. conn on this entry is the authoritative transport identity used by
// the read pump, lifecycle close, and reconnect — it is NOT cleared by a
// transient send error (only by detachIfCurrent / reap / a successful reattach).
type sessionEntry struct {
	runner *agentfleet.Runner
	writer *sessionWriter

	mu           sync.Mutex
	conn         *websocket.Conn // current session-lane conn; nil when detached
	reaped       bool            // true once the PTY has exited; blocks reattach
	reconnecting bool            // true while a CLI reconnect loop is active

	// Freshest params for a CLI-driven re-dial of the session lane. The token is
	// long-lived and reusable; geometry re-applies the viewer's window size.
	token string
	cols  int
	rows  int
}

func newSessionEntry(r *agentfleet.Runner, w *sessionWriter, conn *websocket.Conn, token string, cols, rows int) *sessionEntry {
	return &sessionEntry{runner: r, writer: w, conn: conn, token: token, cols: cols, rows: rows}
}

// currentConn returns the active session-lane conn, or nil when detached.
func (e *sessionEntry) currentConn() *websocket.Conn {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn
}

// isReaped reports whether the PTY has exited.
func (e *sessionEntry) isReaped() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reaped
}

// reattach flushes buffered output to conn and, on success, adopts it as the
// current session-lane, returning the previous conn (which the caller closes).
// The buffer flush and the conn swap happen atomically under the entry lock, so
// a stale detach can never clobber the freshly-attached output and buffered
// bytes are delivered in-order before any subsequent live write.
//
//   - reaped: returns ok=false, err=nil — the PTY exited; do not bind onto a
//     dead runner. The caller closes conn.
//   - flush error: returns ok=false, err!=nil — conn is bad; the backlog is kept
//     buffered for the next attempt. The caller closes conn and retries.
func (e *sessionEntry) reattach(conn *websocket.Conn) (old *websocket.Conn, ok bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reaped {
		return nil, false, nil
	}
	if err := e.writer.attach(conn); err != nil {
		return nil, false, err
	}
	old = e.conn
	e.conn = conn
	return old, true, nil
}

// detachIfCurrent clears the connection only if it is still conn, returning true
// when it did. onDetach, if non-nil, runs (only on a true match) while the entry
// lock is held — it redirects the writer to buffering atomically with the
// detach, so it never clobbers a concurrent re-attach. The PTY keeps running.
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

// reap marks the entry as reaped (its PTY has exited) and returns the current
// conn for the caller to close, clearing it. After reap, reattach refuses, so a
// re-attach racing the delete can't bind onto the dead runner.
func (e *sessionEntry) reap() (conn *websocket.Conn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reaped = true
	conn = e.conn
	e.conn = nil
	return conn
}

// beginReconnect claims the single reconnect slot. It returns false if the entry
// is reaped or a reconnect loop is already running, so at most one loop exists.
func (e *sessionEntry) beginReconnect() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reaped || e.reconnecting {
		return false
	}
	e.reconnecting = true
	return true
}

// endReconnect releases the reconnect slot.
func (e *sessionEntry) endReconnect() {
	e.mu.Lock()
	e.reconnecting = false
	e.mu.Unlock()
}

// setReconnectParams records the freshest token/geometry for a CLI re-dial.
// A non-positive geometry means "not offered" and leaves the known size in
// place: the relay has no viewer size to report when nobody is watching, and
// erasing it would make the next re-dial resize the live PTY to 0x0.
func (e *sessionEntry) setReconnectParams(token string, cols, rows int) {
	e.mu.Lock()
	e.token = token
	if cols > 0 && rows > 0 {
		e.cols, e.rows = cols, rows
	}
	e.mu.Unlock()
}

// reconnectParams returns the token/geometry to use for a CLI re-dial.
func (e *sessionEntry) reconnectParams() (token string, cols, rows int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.token, e.cols, e.rows
}
