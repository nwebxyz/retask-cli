package sandbox

import (
	"bytes"
	"testing"
)

func TestRingBuffer_AppendUnderCap(t *testing.T) {
	r := newRingBuffer(16)
	r.append([]byte("abc"))
	r.append([]byte("de"))
	if got := r.drain(); !bytes.Equal(got, []byte("abcde")) {
		t.Fatalf("drain = %q, want %q", got, "abcde")
	}
	if r.len() != 0 {
		t.Fatalf("len after drain = %d, want 0", r.len())
	}
}

func TestRingBuffer_DropsOldestOverCap(t *testing.T) {
	r := newRingBuffer(5)
	r.append([]byte("abc"))
	r.append([]byte("defg")) // total 7 > 5 → drop oldest 2 ("ab"), keep "cdefg"
	if got := r.drain(); !bytes.Equal(got, []byte("cdefg")) {
		t.Fatalf("drain = %q, want %q", got, "cdefg")
	}
}

func TestRingBuffer_SingleWriteLargerThanCap(t *testing.T) {
	r := newRingBuffer(4)
	r.append([]byte("0123456789")) // keep only last 4 bytes
	if got := r.drain(); !bytes.Equal(got, []byte("6789")) {
		t.Fatalf("drain = %q, want %q", got, "6789")
	}
}

func TestRingBuffer_ZeroCapDropsEverything(t *testing.T) {
	r := newRingBuffer(0)
	r.append([]byte("hello"))
	if r.len() != 0 {
		t.Fatalf("len = %d, want 0 (cap 0 drops)", r.len())
	}
	if got := r.drain(); got != nil {
		t.Fatalf("drain = %q, want nil", got)
	}
}

func TestRingBuffer_EvictionKeepsMostRecentAcrossManyAppends(t *testing.T) {
	r := newRingBuffer(4)
	for _, s := range []string{"aa", "bb", "cc", "dd"} {
		r.append([]byte(s))
	}
	// Only the most recent 4 bytes survive.
	if got := r.drain(); !bytes.Equal(got, []byte("ccdd")) {
		t.Fatalf("drain = %q, want %q", got, "ccdd")
	}
}

func TestRingBuffer_NegativeCapTreatedAsZero(t *testing.T) {
	r := newRingBuffer(-10)
	r.append([]byte("x"))
	if r.len() != 0 {
		t.Fatalf("len = %d, want 0", r.len())
	}
}
