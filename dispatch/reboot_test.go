package dispatch

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reboot-resume §3: the judgement runs once the lease is held, with its
// token, before any other loop: no self-repair pass while it runs, the
// lease renewed meanwhile; then the passes start.
func TestRunJudgesTheRebootFirst(t *testing.T) {
	b, _ := dialRun(t)
	cfg := DefaultConfig()
	cfg.Log = io.Discard
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.MinPTTL = 20*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	release := make(chan struct{})
	var judging atomic.Bool
	var token string
	var mu sync.Mutex
	var passes, passesWhileJudging int
	cfg.Reboot = func(ctx context.Context, tok string) error {
		token = tok
		judging.Store(true)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		judging.Store(false)
		return nil
	}
	cfg.SelfRepair = SelfRepairConfig{Every: 20 * time.Millisecond, Pass: func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		passes++
		if judging.Load() {
			passesWhileJudging++
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b, newFakeRT(nil), cfg) }()
	time.Sleep(time.Second) // well past the lease: it must be renewed meanwhile
	mu.Lock()
	early := passes
	mu.Unlock()
	if early != 0 {
		t.Fatalf("%d self-repair pass(es) before the judgement ended", early)
	}
	close(release)
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v (the lease lost during a long judgement?)", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if passes == 0 || passesWhileJudging != 0 || token == "" {
		t.Fatalf("passes %d (%d while judging), token %q", passes, passesWhileJudging, token)
	}
}

// A failed judgement ends Run with its error (the service starts again and
// judges again), and releases the lease.
func TestRunEndsOnAFailedJudgement(t *testing.T) {
	b, _ := dialRun(t)
	cfg := DefaultConfig()
	cfg.Log = io.Discard
	boom := errors.New("redis gone")
	cfg.Reboot = func(context.Context, string) error { return boom }
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), b, newFakeRT(nil), cfg) }()
	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end (a wait group left waiting?)")
	}
	if ok, err := b.AcquireDispatch(context.Background(), "next", time.Minute); err != nil || !ok {
		t.Fatalf("the lease was not released: %v %v", ok, err)
	}
	// A judgement that lost the lease reads as a lost lease.
	cfg.Reboot = func(context.Context, string) error { return bus.ErrNotHolder }
	_ = b.ReleaseDispatch(context.Background(), "next")
	if err := Run(context.Background(), b, newFakeRT(nil), cfg); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("lost lease: %v", err)
	}
}
