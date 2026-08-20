package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// Reattach answers the relay's reconnect_session: it re-binds a lane to a PTY
// this process is already running, and reports "gone" when it is not.

func TestReattach_UnknownSessionIsGone(t *testing.T) {
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		t.Fatal("must not dial a session this process does not have")
		return nil, nil
	})

	if sm.Reattach(context.Background(), "missing", 0, 0) {
		t.Fatal("Reattach = true, want false (no such session — relay must be told it is gone)")
	}
}

func TestReattach_BootstrappingSessionIsNotGone(t *testing.T) {
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		t.Fatal("must not dial while create() is still bootstrapping")
		return nil, nil
	})
	sm.creating["sess"] = struct{}{}

	// create() is mid-flight and will dial its own lane. Reporting "gone" here
	// would make the relay tell the viewer the session died while it is in fact
	// still starting.
	if !sm.Reattach(context.Background(), "sess", 0, 0) {
		t.Fatal("Reattach = false, want true (session is bootstrapping, not gone)")
	}
}

// The relay does not put a token in reconnect_session: it was minted once at
// new_session, so the copy this process already holds is the value to re-dial
// with.
func TestReattach_KnownSessionRedialsWithItsOwnStoredToken(t *testing.T) {
	var dialed string
	sm := newReconnectTestManager(func(_ context.Context, url string) (*websocket.Conn, error) {
		dialed = url
		return nil, errors.New("dial failed") // stop before the bind
	})
	entry := newTestEntry(new(websocket.Conn))
	entry.setReconnectParams("tok-stable", 0, 0)
	sm.sessions["sess"] = entry

	if !sm.Reattach(context.Background(), "sess", 0, 0) {
		t.Fatal("Reattach = false, want true (session is running here)")
	}
	if !strings.Contains(dialed, "session_id=sess") {
		t.Fatalf("dialed %q, want it to carry session_id=sess", dialed)
	}
	if !strings.Contains(dialed, "token=tok-stable") {
		t.Fatalf("dialed %q, want it to reuse the stored token", dialed)
	}
}

func TestReattach_KeepsStoredTokenForALaterRedial(t *testing.T) {
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		return nil, errors.New("dial failed")
	})
	entry := newTestEntry(new(websocket.Conn))
	entry.setReconnectParams("tok-stable", 0, 0)
	sm.sessions["sess"] = entry

	sm.Reattach(context.Background(), "sess", 120, 40)

	token, cols, rows := entry.reconnectParams()
	if token != "tok-stable" {
		t.Fatalf("token = %q, want the stored tok-stable", token)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("geometry = %dx%d, want 120x40", cols, rows)
	}
}

func TestReattach_KeepsKnownGeometryWhenNoneIsOffered(t *testing.T) {
	sm := newReconnectTestManager(func(context.Context, string) (*websocket.Conn, error) {
		return nil, errors.New("dial failed")
	})
	entry := newTestEntry(new(websocket.Conn))
	entry.setReconnectParams("tok", 120, 40)
	sm.sessions["sess"] = entry

	// The relay has no viewer geometry to offer. That must not erase the size
	// the PTY is actually running at — a later re-dial would resize it to 0x0.
	sm.Reattach(context.Background(), "sess", 0, 0)

	_, cols, rows := entry.reconnectParams()
	if cols != 120 || rows != 40 {
		t.Fatalf("geometry = %dx%d, want the previously known 120x40", cols, rows)
	}
}
