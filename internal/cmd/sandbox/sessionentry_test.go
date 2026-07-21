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

	old := e.swapConn(c2)
	if old != c1 {
		t.Fatalf("swapConn returned %p, want old %p", old, c1)
	}
	if e.currentConn() != c2 {
		t.Fatalf("currentConn = %p, want %p", e.currentConn(), c2)
	}
}

func TestSessionEntry_DetachIfCurrent_Matches(t *testing.T) {
	c := new(websocket.Conn)
	e := newSessionEntry(nil, c)

	if !e.detachIfCurrent(c) {
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
	e.swapConn(c2)
	if e.detachIfCurrent(c1) {
		t.Fatal("detachIfCurrent(stale) = true, want false")
	}
	if e.currentConn() != c2 {
		t.Fatalf("currentConn = %p, want live %p after stale detach", e.currentConn(), c2)
	}
}
