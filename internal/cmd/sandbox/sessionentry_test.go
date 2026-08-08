package sandbox

import (
	"context"
	"testing"

	"github.com/coder/websocket"
)

// newTestEntry builds an entry with a real (empty-buffer) writer so reattach
// exercises the state machine without touching the network — an empty buffer
// means attach performs no conn.Write.
func newTestEntry(conn *websocket.Conn) *sessionEntry {
	w := newSessionWriter(context.Background(), conn, 1024)
	return newSessionEntry(nil, w, conn, "tok", 0, 0)
}

func TestSessionEntry_CurrentConn(t *testing.T) {
	c := new(websocket.Conn)
	e := newTestEntry(c)
	if e.currentConn() != c {
		t.Fatalf("currentConn = %p, want %p", e.currentConn(), c)
	}
}

func TestSessionEntry_Reattach(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newTestEntry(c1)

	old, ok, err := e.reattach(c2)
	if err != nil {
		t.Fatalf("reattach err: %v", err)
	}
	if !ok {
		t.Fatal("reattach should succeed on a non-reaped entry")
	}
	if old != c1 {
		t.Fatalf("reattach returned old %p, want %p", old, c1)
	}
	if e.currentConn() != c2 {
		t.Fatalf("currentConn = %p, want %p", e.currentConn(), c2)
	}
}

func TestSessionEntry_Reap_BlocksReattach(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newTestEntry(c1)

	if got := e.reap(); got != c1 {
		t.Fatalf("reap returned %p, want %p", got, c1)
	}
	if e.currentConn() != nil {
		t.Fatal("conn should be nil after reap")
	}
	old, ok, err := e.reattach(c2)
	if err != nil {
		t.Fatalf("reattach err: %v", err)
	}
	if ok {
		t.Fatal("reattach should refuse (ok=false) after reap")
	}
	if old != nil {
		t.Fatalf("reattach on reaped entry returned old=%p, want nil", old)
	}
}

func TestSessionEntry_DetachIfCurrent_Matches(t *testing.T) {
	c := new(websocket.Conn)
	e := newTestEntry(c)

	if !e.detachIfCurrent(c, nil) {
		t.Fatal("detachIfCurrent(current) = false, want true")
	}
	if e.currentConn() != nil {
		t.Fatal("currentConn should be nil after detach")
	}
}

func TestSessionEntry_DetachIfCurrent_StaleAfterReattach(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newTestEntry(c1)

	// Re-attach installs c2; the old read pump for c1 must NOT detach.
	if _, _, err := e.reattach(c2); err != nil {
		t.Fatalf("reattach err: %v", err)
	}
	if e.detachIfCurrent(c1, nil) {
		t.Fatal("detachIfCurrent(stale) = true, want false")
	}
	if e.currentConn() != c2 {
		t.Fatalf("currentConn = %p, want live %p after stale detach", e.currentConn(), c2)
	}
}

func TestSessionEntry_DetachIfCurrent_CallbackRunsOnMatchOnly(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newTestEntry(c1)

	ran := 0
	// c1 is no longer current after reattaching to c2 → stale detach, no callback.
	if _, _, err := e.reattach(c2); err != nil {
		t.Fatalf("reattach err: %v", err)
	}
	if e.detachIfCurrent(c1, func() { ran++ }) {
		t.Fatal("stale detach should return false")
	}
	if ran != 0 {
		t.Fatalf("callback ran on stale detach: %d", ran)
	}
	// c2 is current → detach succeeds and runs the callback exactly once.
	if !e.detachIfCurrent(c2, func() { ran++ }) {
		t.Fatal("current detach should return true")
	}
	if ran != 1 {
		t.Fatalf("callback should run once, ran %d", ran)
	}
}

func TestSessionEntry_BeginReconnect_SingleSlot(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	if !e.beginReconnect() {
		t.Fatal("first beginReconnect should succeed")
	}
	if e.beginReconnect() {
		t.Fatal("second beginReconnect should fail while one is active")
	}
	e.endReconnect()
	if !e.beginReconnect() {
		t.Fatal("beginReconnect should succeed again after endReconnect")
	}
}

func TestSessionEntry_BeginReconnect_RefusedAfterReap(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.reap()
	if e.beginReconnect() {
		t.Fatal("beginReconnect should refuse on a reaped entry")
	}
}

func TestSessionEntry_ReconnectParams(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.setReconnectParams("fresh", 120, 40)
	tok, cols, rows := e.reconnectParams()
	if tok != "fresh" || cols != 120 || rows != 40 {
		t.Fatalf("params = %q/%d/%d, want fresh/120/40", tok, cols, rows)
	}
}
