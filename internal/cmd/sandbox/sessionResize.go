package sandbox

import (
	"sync"
	"time"
)

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
