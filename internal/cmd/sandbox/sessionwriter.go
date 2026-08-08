package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// flushChunkBytes bounds the raw size of each data frame emitted while
	// flushing the buffer, so a large backlog is delivered as several
	// modestly-sized session-lane messages rather than one giant frame the
	// peer might reject.
	flushChunkBytes = 32 * 1024
	// sessionFlushTimeout bounds how long a buffer flush may block. attach runs
	// under the entry lock, so an unbounded flush to a slow peer would stall
	// detach/reap; on timeout the flush fails and the conn is retried.
	sessionFlushTimeout = 10 * time.Second
)

// wsConn is the subset of *websocket.Conn that sessionWriter needs. Abstracting
// it keeps the writer unit-testable without a live socket; *websocket.Conn
// satisfies it directly.
type wsConn interface {
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	CloseNow() error
}

// sessionWriter is a session's PERMANENT output sink, set as the runner's output
// once at create and never re-pointed. It tracks the current session-lane conn
// (nil when detached) and a bounded ring buffer. While attached, PTY output is
// framed and written straight to the conn; on a send error or while detached it
// is buffered (drop-oldest) so the reconnect path can flush it to the next conn.
//
// Writes never fail from the runner's view — a dead session-lane socket must
// never stall or error PTY output.
type sessionWriter struct {
	ctx context.Context

	mu   sync.Mutex
	conn wsConn // current session-lane; nil when detached
	buf  *ringBuffer
}

func newSessionWriter(ctx context.Context, conn wsConn, bufBytes int) *sessionWriter {
	return &sessionWriter{ctx: ctx, conn: conn, buf: newRingBuffer(bufBytes)}
}

// Write frames p as a session-lane data message and sends it to the current
// conn, or buffers it when detached / on a send error. It always reports success.
func (w *sessionWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn != nil {
		if err := w.conn.Write(w.ctx, websocket.MessageText, frameData(p)); err != nil {
			// The socket died mid-write. Drop it and close it so the read pump
			// wakes and runs the normal detach → reconnect path, and buffer this
			// chunk so it can be re-delivered. Never surface the error.
			w.conn.CloseNow() //nolint:errcheck
			w.conn = nil
			w.buf.append(p)
		}
		return len(p), nil
	}
	w.buf.append(p)
	return len(p), nil
}

// attach flushes any buffered output to conn IN ORDER, then adopts it as the
// current sink so subsequent live writes follow the flushed backlog. It runs
// under the writer lock, excluding concurrent Write, so ordering holds. If the
// flush fails, the un-sent remainder is re-staged, conn is NOT adopted, and the
// error is returned so the caller can discard conn and retry.
func (w *sessionWriter) attach(conn wsConn) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	data := w.buf.drain()
	if len(data) > 0 {
		ctx, cancel := context.WithTimeout(w.ctx, sessionFlushTimeout)
		defer cancel()
		for i := 0; i < len(data); i += flushChunkBytes {
			end := min(i+flushChunkBytes, len(data))
			if err := conn.Write(ctx, websocket.MessageText, frameData(data[i:end])); err != nil {
				w.buf.append(data[i:]) // re-stage the un-sent tail, in order
				return err
			}
		}
	}
	w.conn = conn
	return nil
}

// detach stops sending to the socket; subsequent writes buffer until re-attach.
func (w *sessionWriter) detach() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn = nil
}

// buffered reports the number of bytes currently held (for tests/metrics).
func (w *sessionWriter) buffered() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.len()
}

// frameData encodes raw PTY bytes as a base64 session-lane "data" message,
// matching the framing the browser terminal expects.
func frameData(p []byte) []byte {
	msg, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"data", base64.StdEncoding.EncodeToString(p)})
	return msg
}
