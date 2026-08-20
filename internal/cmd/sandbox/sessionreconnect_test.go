package sandbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newReconnectTestManager builds a minimal manager for exercising the reconnect
// loop: tiny backoff and an injectable dialer, no fleet/runner needed.
func newReconnectTestManager(dial wsDialer) *SessionManager {
	return &SessionManager{
		wsBase:           "wss://test",
		dial:             dial,
		reconnectInitial: time.Millisecond,
		reconnectMax:     time.Millisecond,
		sessions:         make(map[string]*sessionEntry),
		creating:         make(map[string]struct{}),
	}
}

func TestReconnectLoop_ExitsWhenAlreadyAttached(t *testing.T) {
	var dialed int
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		dialed++
		return nil, errors.New("should not dial")
	})
	// Entry still has a live conn → nothing to reconnect.
	e := newTestEntry(new(websocket.Conn))

	done := make(chan struct{})
	go func() { sm.reconnectLoop(context.Background(), e, "sess"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnectLoop did not return promptly when already attached")
	}
	if dialed != 0 {
		t.Fatalf("dialed = %d, want 0 (already attached)", dialed)
	}
}

func TestReconnectLoop_RetriesOnDialErrorThenStopsOnReap(t *testing.T) {
	var mu sync.Mutex
	var dialed int

	e := newTestEntry(new(websocket.Conn))
	e.detachIfCurrent(e.currentConn(), nil) // drop → detached, not reaped

	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		mu.Lock()
		dialed++
		d := dialed
		mu.Unlock()
		if d == 3 {
			e.reap() // stop the loop after a few attempts
		}
		return nil, errors.New("dial failed")
	})

	done := make(chan struct{})
	go func() { sm.reconnectLoop(context.Background(), e, "sess"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not terminate after reap")
	}

	mu.Lock()
	got := dialed
	mu.Unlock()
	if got != 3 {
		t.Fatalf("dialed = %d, want 3 (retried until reaped)", got)
	}
}

func TestReconnectLoop_ExitsOnContextCancel(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.detachIfCurrent(e.currentConn(), nil)

	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		return nil, errors.New("dial failed")
	})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { sm.reconnectLoop(ctx, e, "sess"); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not exit on context cancel")
	}
}

func TestStartReconnect_RefusedAfterReap(t *testing.T) {
	e := newTestEntry(new(websocket.Conn))
	e.reap()
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		t.Fatal("dial must not be called for a reaped entry")
		return nil, nil
	})
	// Should be a no-op (beginReconnect refuses); no goroutine, no dial.
	sm.startReconnect(context.Background(), e, "sess")
	time.Sleep(10 * time.Millisecond)
}
