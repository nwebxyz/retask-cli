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

	mu   sync.Mutex
	conn *websocket.Conn // current session-lane conn; nil when detached
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
// one (which the caller should close). Used on re-attach.
func (e *sessionEntry) swapConn(conn *websocket.Conn) (old *websocket.Conn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old = e.conn
	e.conn = conn
	return old
}

// detachIfCurrent clears the connection only if it is still conn, returning
// true when it did. A read pump calls this when its socket dies so it does not
// clobber a newer connection installed by a concurrent re-attach.
func (e *sessionEntry) detachIfCurrent(conn *websocket.Conn) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn == conn {
		e.conn = nil
		return true
	}
	return false
}
