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

// A live resize (updateSize, called from readLoop on an already-attached
// lane) must be what a later reattach sees — not the geometry the lane was
// last (re)dialed at. Before updateSize existed, a resize moved the live PTY
// but never touched reconnectParams, so the NEXT reattach (the CLI's own
// reconnectLoop, or a relay-driven Reattach) replayed the stale dial-time
// size into bindReattach's `entry.runner.Resize(rows, cols)` and silently
// shrank the PTY back down, even though the browser's window never changed.
func TestSessionEntry_UpdateSize_SurvivesIntoReconnectParams(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.setReconnectParams("tok", 80, 24) // size at session start

	e.updateSize(150, 45) // browser resized while the lane was live

	tok, cols, rows := e.reconnectParams()
	if tok != "tok" || cols != 150 || rows != 45 {
		t.Fatalf("params = %q/%d/%d, want tok/150/45 (the live size, not the stale 80/24 from session start)", tok, cols, rows)
	}
}

// A later re-dial (setReconnectParams, e.g. a relay-driven Reattach carrying
// its own geometry) must still be able to override updateSize's value — the
// freshest source wins either way, updateSize is not sticky.
func TestSessionEntry_UpdateSize_ThenSetReconnectParams(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.setReconnectParams("tok", 80, 24)
	e.updateSize(150, 45)
	e.setReconnectParams("tok2", 200, 60)

	tok, cols, rows := e.reconnectParams()
	if tok != "tok2" || cols != 200 || rows != 60 {
		t.Fatalf("params = %q/%d/%d, want tok2/200/60", tok, cols, rows)
	}
}

// updateSize must not rotate the session-lane token — only setReconnectParams
// does that, on an actual re-dial.
func TestSessionEntry_UpdateSize_LeavesTokenAlone(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.setReconnectParams("tok", 80, 24)
	e.updateSize(150, 45)

	tok, _, _ := e.reconnectParams()
	if tok != "tok" {
		t.Fatalf("token = %q, want unchanged %q", tok, "tok")
	}
}
