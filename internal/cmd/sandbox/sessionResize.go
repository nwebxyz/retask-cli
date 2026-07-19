package sandbox

import (
	"encoding/json"
	"sync"
	"time"
)

// maxPTYDimension is the upper bound (inclusive) on a valid PTY dimension.
// TIOCSWINSZ's fields are uint16, and a latched size is replayed to every
// future VM attach, so a value above the bound is clamped down to it rather
// than applied unbounded — a huge number plausibly means a real large
// terminal. A value at or below zero is still treated as invalid, the same
// as an absent one: it is nonsense, not a size to round up.
const maxPTYDimension = 1000

// clampDimension caps a PTY dimension at maxPTYDimension. It must only be
// called with values already known to be positive (> 0); non-positive
// values are rejected earlier, not clamped.
func clampDimension(v int) int {
	if v > maxPTYDimension {
		return maxPTYDimension
	}
	return v
}

// pendingSize holds a window size that arrived before the PTY could accept it.
//
// Resize frames can reach us during bootstrap (repo clones, env setup), long
// before the PTY exists. Without this the frame is discarded and the session
// stays at its default geometry until the user manually resizes the browser.
type pendingSize struct {
	mu   sync.Mutex
	rows int
	cols int
	set  bool
}

// store records the most recent size, replacing any earlier unapplied one.
func (p *pendingSize) store(rows, cols int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows, p.cols, p.set = rows, cols, true
}

// take returns the recorded size and clears it. ok is false when nothing is
// pending.
func (p *pendingSize) take() (rows, cols int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.set {
		return 0, 0, false
	}
	rows, cols, ok = p.rows, p.cols, true
	p.rows, p.cols, p.set = 0, 0, false
	return rows, cols, ok
}

// resizer is the subset of agentfleet.Runner used for resizing, so the retry
// logic can be tested without a real PTY.
type resizer interface {
	Resize(rows, cols int) error
}

// applyResize retries because Runner.Start is non-blocking: the PTY is opened
// on a goroutine, so a resize issued right after Start can land while the
// master fd is still nil and come back as os.ErrClosed.
func applyResize(r resizer, rows, cols, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = r.Resize(rows, cols); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return err
}

// recordResizeFrame reports whether raw is a resize frame, latching its size
// into p when it is. p may be nil, in which case the frame is classified but
// not recorded.
func recordResizeFrame(raw []byte, p *pendingSize) bool {
	var msg struct {
		Type string `json:"type"`
		Rows int    `json:"rows"`
		Cols int    `json:"cols"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Type != "resize" {
		return false
	}
	if msg.Rows <= 0 || msg.Cols <= 0 {
		return false
	}
	if p != nil {
		p.store(clampDimension(msg.Rows), clampDimension(msg.Cols))
	}
	return true
}
