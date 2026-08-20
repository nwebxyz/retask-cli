package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// fakeConn records frames written to it and can be told to fail writes.
type fakeConn struct {
	mu       sync.Mutex
	frames   [][]byte
	failNext bool // when true, the next Write returns an error
	closed   int
}

func (c *fakeConn) Write(_ context.Context, _ websocket.MessageType, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failNext {
		c.failNext = false
		return errors.New("write failed")
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	c.frames = append(c.frames, cp)
	return nil
}

func (c *fakeConn) CloseNow() error {
	c.mu.Lock()
	c.closed++
	c.mu.Unlock()
	return nil
}

// decoded concatenates the raw bytes carried by all recorded data frames.
func (c *fakeConn) decoded(t *testing.T) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, f := range c.frames {
		var m struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal(f, &m); err != nil {
			t.Fatalf("frame not JSON: %v", err)
		}
		b, err := base64.StdEncoding.DecodeString(m.Data)
		if err != nil {
			t.Fatalf("frame data not base64: %v", err)
		}
		out = append(out, b...)
	}
	return out
}

func TestSessionWriter_WriteWhenAttached(t *testing.T) {
	c := &fakeConn{}
	w := newSessionWriter(context.Background(), c, 1024)
	w.Write([]byte("hello"))
	if got := string(c.decoded(t)); got != "hello" {
		t.Fatalf("conn got %q, want %q", got, "hello")
	}
	if w.buffered() != 0 {
		t.Fatalf("buffered = %d, want 0", w.buffered())
	}
}

func TestSessionWriter_WriteWhenDetachedBuffers(t *testing.T) {
	w := newSessionWriter(context.Background(), nil, 1024)
	w.Write([]byte("abc"))
	if w.buffered() != 3 {
		t.Fatalf("buffered = %d, want 3", w.buffered())
	}
}

func TestSessionWriter_SendErrorBuffersAndCloses(t *testing.T) {
	c := &fakeConn{failNext: true}
	w := newSessionWriter(context.Background(), c, 1024)
	w.Write([]byte("lost")) // conn.Write fails → buffer + close + detach
	if w.buffered() != 4 {
		t.Fatalf("buffered = %d, want 4", w.buffered())
	}
	if c.closed != 1 {
		t.Fatalf("CloseNow calls = %d, want 1", c.closed)
	}
	// A subsequent write must go straight to the buffer (conn was dropped).
	w.Write([]byte("more"))
	if w.buffered() != 8 {
		t.Fatalf("buffered = %d, want 8", w.buffered())
	}
}

func TestSessionWriter_AttachFlushesInOrderBeforeLive(t *testing.T) {
	w := newSessionWriter(context.Background(), nil, 1024)
	w.Write([]byte("gap1"))
	w.Write([]byte("gap2"))

	c := &fakeConn{}
	if err := w.attach(c); err != nil {
		t.Fatalf("attach: %v", err)
	}
	w.Write([]byte("live"))

	if got := string(c.decoded(t)); got != "gap1gap2live" {
		t.Fatalf("conn got %q, want %q", got, "gap1gap2live")
	}
	if w.buffered() != 0 {
		t.Fatalf("buffered = %d, want 0 after flush", w.buffered())
	}
}

func TestSessionWriter_AttachEmptyBufferNoFrames(t *testing.T) {
	w := newSessionWriter(context.Background(), nil, 1024)
	c := &fakeConn{}
	if err := w.attach(c); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(c.frames) != 0 {
		t.Fatalf("frames = %d, want 0 (nothing buffered)", len(c.frames))
	}
}

func TestSessionWriter_AttachFlushErrorReStagesAndRejects(t *testing.T) {
	w := newSessionWriter(context.Background(), nil, 1024)
	w.Write([]byte("keepme"))

	c := &fakeConn{failNext: true}
	if err := w.attach(c); err == nil {
		t.Fatal("attach should return the flush error")
	}
	if w.buffered() != 6 {
		t.Fatalf("buffered = %d, want 6 (re-staged)", w.buffered())
	}
	// conn was not adopted: a live write still buffers rather than reaching c.
	w.Write([]byte("x"))
	if len(c.frames) != 0 {
		t.Fatalf("conn adopted despite flush failure: %d frames", len(c.frames))
	}
}

func TestSessionWriter_ZeroCapDropsWhileDetached(t *testing.T) {
	w := newSessionWriter(context.Background(), nil, 0)
	w.Write([]byte("dropped"))
	if w.buffered() != 0 {
		t.Fatalf("buffered = %d, want 0 (cap 0)", w.buffered())
	}
	c := &fakeConn{}
	if err := w.attach(c); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(c.frames) != 0 {
		t.Fatalf("frames = %d, want 0", len(c.frames))
	}
}
