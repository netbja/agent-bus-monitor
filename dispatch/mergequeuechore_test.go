package dispatch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// merge-popup-setting (the person's choice A): a delivery marks the merge
// queue due, and the chore runs a pass at its next check (seconds), not at
// the next 5-minute tick; the same mark never runs a second pass.
func TestMergeQueueRunsWhenMarkedDue(t *testing.T) {
	b, _ := dialRun(t)
	var passes atomic.Int32
	var due atomic.Int64
	cfg := fastConfig()
	cfg.MergeQueue = MergeQueueConfig{
		Every: time.Hour,
		Poll:  20 * time.Millisecond,
		Pass:  func(context.Context, string) error { passes.Add(1); return nil },
		Due:   func(context.Context) (int64, error) { return due.Load(), nil },
	}
	start(t, b, newFakeRT(alwaysReady), cfg)
	waitFor(t, 3*time.Second, "the first pass", func() bool { return passes.Load() == 1 })
	due.Store(1794955000000)
	waitFor(t, time.Second, "a pass for the mark", func() bool { return passes.Load() == 2 })
	time.Sleep(200 * time.Millisecond)
	if n := passes.Load(); n != 2 {
		t.Fatalf("the same mark ran %d passes in all, want 2", n)
	}
	due.Store(1794955000001)
	waitFor(t, time.Second, "a pass for a new mark", func() bool { return passes.Load() == 3 })
}
