package sandbox

import (
	"testing"

	"github.com/coder/websocket"
)

func TestSessionEntry_CurrentConn(t *testing.T) {
	c := new(websocket.Conn)
	e := newSessionEntry(nil, c)
	if e.currentConn() != c {
		t.Fatalf("currentConn = %p, want %p", e.currentConn(), c)
	}
}

func TestSessionEntry_SwapConn(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newSessionEntry(nil, c1)

	old, ok := e.swapConn(c2, nil)
	if !ok {
		t.Fatal("swapConn should succeed on a non-reaped entry")
	}
	if old != c1 {
		t.Fatalf("swapConn returned %p, want old %p", old, c1)
	}
	if e.currentConn() != c2 {
		t.Fatalf("currentConn = %p, want %p", e.currentConn(), c2)
	}
}

func TestSessionEntry_Reap_BlocksSwap(t *testing.T) {
	c1 := new(websocket.Conn)
	c2 := new(websocket.Conn)
	e := newSessionEntry(nil, c1)

	if got := e.reap(); got != c1 {
		t.Fatalf("reap returned %p, want %p", got, c1)
	}
	if e.currentConn() != nil {
		t.Fatal("conn should be nil after reap")
	}
	old, ok := e.swapConn(c2, nil)
	if ok {
		t.Fatal("swapConn should refuse (ok=false) after reap")
	}
	if old != nil {
		t.Fatalf("swapConn on reaped entry returned old=%p, want nil", old)
	}
}

func TestSessionEntry_DetachIfCurrent_Matches(t *testing.T) {
	c := new(websocket.Conn)
	e := newSessionEntry(nil, c)

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
	e := newSessionEntry(nil, c1)

	// Re-attach installs c2; the old read pump for c1 must NOT detach.
	e.swapConn(c2, nil)
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
	e := newSessionEntry(nil, c1)

	ran := 0
	// c1 is no longer current after swapping to c2 → stale detach, no callback.
	e.swapConn(c2, nil)
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
