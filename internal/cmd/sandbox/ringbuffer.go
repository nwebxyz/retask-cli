package sandbox

// ringBuffer is a bounded FIFO byte buffer. When an append would exceed cap it
// drops the OLDEST bytes to make room, so the buffer always holds the most
// recent cap bytes — the right policy for terminal output, where the newest
// output is what a reconnecting viewer wants to see. A cap of 0 disables
// buffering: every append is dropped immediately.
//
// It is NOT safe for concurrent use; sessionWriter guards it with a mutex.
type ringBuffer struct {
	buf []byte
	cap int
}

func newRingBuffer(capBytes int) *ringBuffer {
	if capBytes < 0 {
		capBytes = 0
	}
	return &ringBuffer{cap: capBytes}
}

// append copies p into the buffer, evicting the oldest bytes as needed so the
// total never exceeds cap. When len(p) >= cap only the last cap bytes of p are
// kept. cap 0 drops everything.
func (r *ringBuffer) append(p []byte) {
	if r.cap == 0 || len(p) == 0 {
		return
	}
	if len(p) >= r.cap {
		// p alone fills (or overfills) the buffer: keep only its tail.
		r.buf = append(r.buf[:0], p[len(p)-r.cap:]...)
		return
	}
	if len(r.buf)+len(p) > r.cap {
		// Evict the oldest bytes by shifting the retained tail to the front,
		// in place, so the backing array stays bounded across evictions.
		drop := len(r.buf) + len(p) - r.cap
		n := copy(r.buf, r.buf[drop:])
		r.buf = r.buf[:n]
	}
	r.buf = append(r.buf, p...)
}

// drain returns a copy of all buffered bytes and empties the buffer.
func (r *ringBuffer) drain() []byte {
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	r.buf = r.buf[:0]
	return out
}

// len reports the number of bytes currently buffered.
func (r *ringBuffer) len() int { return len(r.buf) }
