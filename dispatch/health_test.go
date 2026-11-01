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
	check := HealthCheck{Definition: d, Run: func(ctx context.Context) (health.Outcome, error) {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		calls.Add(1)
		defer active.Add(-1)
		select {
		case <-ctx.Done():
			return health.Outcome{Status: health.Unknown}, ctx.Err()
		case <-time.After(35 * time.Millisecond):
			return health.Outcome{Status: health.OK, Detail: "service check complete"}, nil
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
	l := &loop{b: b, token: "stale-holder", cfg: Config{EscalateTo: "master", Log: io.Discard, Health: HealthConfig{Checks: []HealthCheck{{Definition: d, Run: func(context.Context) (health.Outcome, error) {
		return health.Outcome{Status: health.OK, Detail: "ok"}, nil
	}}}}}}
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

// redis-watch: the latest-release check runs once a day, however many health
// passes run, and its failure never stops the loop or its checks.
func TestHealthLoopRunsRedisRefreshDaily(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "health-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	d, _ := health.ByID("services")
	d.Interval = time.Millisecond
	var refreshes, checks atomic.Int32
	check := HealthCheck{Definition: d, Run: func(context.Context) (health.Outcome, error) {
		checks.Add(1)
		return health.Outcome{Status: health.OK, Detail: "service check complete"}, nil
	}}
	refresh := func(context.Context) error { refreshes.Add(1); return io.ErrUnexpectedEOF }
	l := &loop{b: b, token: "health-holder", cfg: Config{EscalateTo: "master", Log: io.Discard,
		Health: HealthConfig{Checks: []HealthCheck{check}, Tick: time.Millisecond, PassBudget: time.Second, RedisRefresh: refresh}}}
	runCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err := l.healthLoop(runCtx)
	cancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("healthLoop = %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("redis refresh calls = %d, want 1", refreshes.Load())
	}
	if checks.Load() < 3 {
		t.Fatalf("a failed refresh stopped the checks: %d calls", checks.Load())
	}
}

// The evaluator's Person reaches the stored record for a warn or fail only: a
// spent restart budget is for the person (reviewer-codex), and an error or an
// unsafe detail, read as unknown, never carries it.
func TestHealthLoopStoresThePersonsOutcome(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "health-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		out  health.Outcome
		err  error
		want bool
	}{
		{health.Outcome{Status: health.Fail, Detail: "service restart budget is spent", Person: true}, nil, true},
		{health.Outcome{Status: health.OK, Detail: "installed services are active", Person: true}, nil, false},
		{health.Outcome{Status: health.Fail, Detail: "service restart budget is spent", Person: true}, io.ErrUnexpectedEOF, false},
		{health.Outcome{Status: health.Fail, Detail: "token: sk-ant-0123456789abcdef", Person: true}, nil, false},
	} {
		d, _ := health.ByID("services")
		d.Interval = time.Hour
		out, runErr := c.out, c.err
		check := HealthCheck{Definition: d, Run: func(context.Context) (health.Outcome, error) { return out, runErr }}
		l := &loop{b: b, token: "health-holder", cfg: Config{EscalateTo: "master", Log: io.Discard,
			Health: HealthConfig{Checks: []HealthCheck{check}, Tick: time.Millisecond, PassBudget: time.Second}}}
		runCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		_ = l.healthLoop(runCtx)
		cancel()
		recs, err := b.Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := recs["services"].Person; got != c.want {
			t.Errorf("%+v (err %v): stored person %v, want %v", c.out, c.err, got, c.want)
		}
	}
}
