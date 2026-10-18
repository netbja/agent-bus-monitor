package dispatch

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

func TestHealthLoopRunsChecksSeriallyAndStopsOnCancellation(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "health-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	d, _ := health.ByID("services")
	d.Interval = 10 * time.Millisecond
	var active, maximum, calls atomic.Int32
	check := HealthCheck{Definition: d, Run: func(ctx context.Context) (health.Status, string, error) {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		calls.Add(1)
		defer active.Add(-1)
		select {
		case <-ctx.Done():
			return health.Unknown, "", ctx.Err()
		case <-time.After(35 * time.Millisecond):
			return health.OK, "service check complete", nil
		}
	}}
	l := &loop{b: b, token: "health-holder", cfg: Config{EscalateTo: "master", Log: io.Discard, Health: HealthConfig{Checks: []HealthCheck{check}, Tick: time.Millisecond, PassBudget: time.Second}}}
	runCtx, cancel := context.WithTimeout(ctx, 130*time.Millisecond)
	err := l.healthLoop(runCtx)
	cancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("healthLoop = %v", err)
	}
	if calls.Load() < 2 {
		t.Fatalf("check calls = %d", calls.Load())
	}
	if maximum.Load() != 1 {
		t.Fatalf("overlapping check calls: %d", maximum.Load())
	}
	records, err := b.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if records[d.ID].Status != health.OK {
		t.Fatalf("stored status = %s", records[d.ID].Status)
	}
}

func TestHealthLoopStopsWritingAfterLeaseLoss(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "real-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	d, _ := health.ByID("services")
	l := &loop{b: b, token: "stale-holder", cfg: Config{EscalateTo: "master", Log: io.Discard, Health: HealthConfig{Checks: []HealthCheck{{Definition: d, Run: func(context.Context) (health.Status, string, error) { return health.OK, "ok", nil }}}}}}
	if err := l.healthLoop(ctx); err != ErrLeaseLost {
		t.Fatalf("healthLoop = %v", err)
	}
	records, err := b.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("stale holder wrote health records: %+v", records)
	}
}
