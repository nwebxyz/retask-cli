package sandbox

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPendingSizeTakeEmpty(t *testing.T) {
	var p pendingSize
	if _, _, ok := p.take(); ok {
		t.Fatal("expected take() on an empty pendingSize to report ok=false")
	}
}

func TestPendingSizeStoreThenTake(t *testing.T) {
	var p pendingSize
	p.store(30, 120)
	rows, cols, ok := p.take()
	if !ok || rows != 30 || cols != 120 {
		t.Fatalf("got rows=%d cols=%d ok=%v, want 30/120/true", rows, cols, ok)
	}
}

func TestPendingSizeTakeClears(t *testing.T) {
	var p pendingSize
	p.store(30, 120)
	p.take()
	if _, _, ok := p.take(); ok {
		t.Fatal("expected the second take() to report ok=false")
	}
}

func TestPendingSizeKeepsLatest(t *testing.T) {
	var p pendingSize
	p.store(30, 120)
	p.store(40, 100)
	rows, cols, _ := p.take()
	if rows != 40 || cols != 100 {
		t.Fatalf("got rows=%d cols=%d, want the most recent 40/100", rows, cols)
	}
}

func TestPendingSizeConcurrent(t *testing.T) {
	var p pendingSize
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); p.store(30, 120) }()
		go func() { defer wg.Done(); p.take() }()
	}
	wg.Wait() // -race must report no data race
}

type fakeResizer struct {
	failures int
	calls    int
	rows     int
	cols     int
}

func (f *fakeResizer) Resize(rows, cols int) error {
	f.calls++
	if f.calls <= f.failures {
		return os.ErrClosed
	}
	f.rows, f.cols = rows, cols
	return nil
}

func TestApplyResizeSucceedsFirstTry(t *testing.T) {
	f := &fakeResizer{}
	if err := applyResize(f, 30, 120, 5, time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 1 || f.rows != 30 || f.cols != 120 {
		t.Fatalf("got calls=%d rows=%d cols=%d, want 1/30/120", f.calls, f.rows, f.cols)
	}
}

func TestApplyResizeRetriesUntilPtyExists(t *testing.T) {
	f := &fakeResizer{failures: 3}
	if err := applyResize(f, 30, 120, 5, time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 4 || f.rows != 30 {
		t.Fatalf("got calls=%d rows=%d, want 4/30", f.calls, f.rows)
	}
}

func TestApplyResizeGivesUpAfterAttempts(t *testing.T) {
	f := &fakeResizer{failures: 99}
	err := applyResize(f, 30, 120, 3, time.Millisecond)
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("got %v, want os.ErrClosed", err)
	}
	if f.calls != 3 {
		t.Fatalf("got calls=%d, want exactly 3", f.calls)
	}
}

func TestRecordResizeFrameLatchesSize(t *testing.T) {
	var p pendingSize
	if !recordResizeFrame([]byte(`{"type":"resize","cols":120,"rows":30}`), &p) {
		t.Fatal("expected a resize frame to be recognized")
	}
	rows, cols, ok := p.take()
	if !ok || rows != 30 || cols != 120 {
		t.Fatalf("got rows=%d cols=%d ok=%v, want 30/120/true", rows, cols, ok)
	}
}

func TestRecordResizeFrameIgnoresOtherFrames(t *testing.T) {
	var p pendingSize
	if recordResizeFrame([]byte(`{"type":"data","data":"aGk="}`), &p) {
		t.Fatal("data frames must not be treated as resizes")
	}
	if recordResizeFrame([]byte(`not json`), &p) {
		t.Fatal("malformed frames must not be treated as resizes")
	}
	if _, _, ok := p.take(); ok {
		t.Fatal("nothing should have been latched")
	}
}

func TestRecordResizeFrameIgnoresNonPositive(t *testing.T) {
	var p pendingSize
	if recordResizeFrame([]byte(`{"type":"resize","cols":0,"rows":30}`), &p) {
		t.Fatal("a zero dimension must be rejected")
	}
	if _, _, ok := p.take(); ok {
		t.Fatal("nothing should have been latched")
	}
}

func TestRecordResizeFrameIgnoresOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"zero rows", `{"type":"resize","cols":120,"rows":0}`},
		{"negative cols", `{"type":"resize","cols":-1,"rows":30}`},
		{"negative rows", `{"type":"resize","cols":120,"rows":-1}`},
		{"cols above max", `{"type":"resize","cols":1001,"rows":30}`},
		{"rows above max", `{"type":"resize","cols":120,"rows":1001}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p pendingSize
			if recordResizeFrame([]byte(tc.raw), &p) {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
			if _, _, ok := p.take(); ok {
				t.Fatalf("nothing should have been latched for %s", tc.name)
			}
		})
	}
}

func TestRecordResizeFrameAcceptsMaxBoundary(t *testing.T) {
	var p pendingSize
	if !recordResizeFrame([]byte(`{"type":"resize","cols":1000,"rows":1000}`), &p) {
		t.Fatal("a boundary size of exactly 1000 must be accepted")
	}
	rows, cols, ok := p.take()
	if !ok || rows != 1000 || cols != 1000 {
		t.Fatalf("got rows=%d cols=%d ok=%v, want 1000/1000/true", rows, cols, ok)
	}
}

func TestRecordResizeFrameToleratesNilHolder(t *testing.T) {
	if !recordResizeFrame([]byte(`{"type":"resize","cols":120,"rows":30}`), nil) {
		t.Fatal("a nil holder must still classify the frame as a resize")
	}
}
