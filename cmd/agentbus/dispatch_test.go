package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// dialDispatchMain is dialMain plus a DeleteProject cleanup (dispatch writes
// lease and wake keys dialMain does not know about).
func dialDispatchMain(t *testing.T) *bus.Bus {
	t.Helper()
	b, r := dialMain(t)
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	return b
}

// noRuntime fails the test if the dispatcher ever reaches a pane.
type noRuntime struct{ t *testing.T }

func (n noRuntime) Inspect(context.Context, string) dispatch.Inspection {
	n.t.Error("Inspect called")
	return dispatch.Inspection{}
}
func (n noRuntime) Prompt(context.Context, string, string, string) (bool, error) {
	n.t.Error("Prompt called")
	return false, nil
}

func TestDispatchStatusExitCodes(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	var out, errb bytes.Buffer
	if code := runDispatch(ctx, b, []string{"--status"}, noRuntime{t}, &out, &errb); code != 1 || strings.TrimSpace(out.String()) != "dispatch down" {
		t.Fatalf("status without lease = %d %q, want 1 \"dispatch down\"", code, out.String())
	}
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	out.Reset()
	if code := runDispatch(ctx, b, []string{"--status"}, noRuntime{t}, &out, &errb); code != 0 || strings.TrimSpace(out.String()) != "dispatch up" {
		t.Fatalf("status with lease = %d %q, want 0 \"dispatch up\"", code, out.String())
	}
}

func TestDispatchAlreadyRunningExit0(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	if ok, _ := b.AcquireDispatch(ctx, "other", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	var out, errb bytes.Buffer
	if code := runDispatch(ctx, b, nil, noRuntime{t}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "already running") {
		t.Fatalf("stderr = %q, want an already-running message", errb.String())
	}
}

func TestDispatchBadFlags(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"--horizon", "soon"}, {"--escalate-to", "Bad Name"}, {"--bogus"}, {"--escalate-after"}} {
		errb.Reset()
		if code := runDispatch(context.Background(), nil, args, nil, &out, &errb); code != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage)", args, code)
		}
	}
}

// fastDispatch shrinks the lease timings so lease-loss tests run in seconds.
func fastDispatch(t *testing.T) {
	t.Helper()
	old := dispatchBaseConfig
	dispatchBaseConfig = func() dispatch.Config {
		c := old()
		c.Lease, c.Renew, c.MinPTTL, c.Tick = time.Second, 100*time.Millisecond, 200*time.Millisecond, 100*time.Millisecond
		return c
	}
	t.Cleanup(func() { dispatchBaseConfig = old })
}

// A dispatcher whose lease vanished (suspend, broker restart) while its project
// lives on re-acquires instead of exiting for good.
func TestDispatchReacquiresAfterLeaseLoss(t *testing.T) {
	fastDispatch(t)
	b, r := dialMain(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	if _, err := b.Notify(ctx, "me", "project is alive"); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out, errb bytes.Buffer
	go func() { done <- runDispatch(ctx, b, nil, noRuntime{t}, &out, &errb) }()
	key := bus.DispatchKey(b.Project())
	waitKey := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if n, _ := r.Exists(context.Background(), key).Result(); (n == 1) == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("lease present=%v not reached in 5s", want)
	}
	waitKey(true)
	first, _ := r.Get(ctx, key).Result()
	if err := r.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		tok, _ := r.Get(ctx, key).Result()
		if tok != "" && tok != first {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("dispatcher exited (%d) after lease loss; stderr %q", code, errb.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatcher did not re-acquire its lease within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit after cancel = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not stop on cancel")
	}
}

// A deleted project ends the dispatcher (exit 0) and nothing is recreated.
func TestDispatchExitsWhenProjectDeleted(t *testing.T) {
	fastDispatch(t)
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	if _, err := b.Notify(ctx, "me", "project is alive"); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out, errb bytes.Buffer
	go func() { done <- runDispatch(ctx, b, nil, noRuntime{t}, &out, &errb) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := r.Exists(ctx, bus.DispatchKey(b.Project())).Result(); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatcher never took its lease")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := bus.DeleteProject(ctx, r, b.Project()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr %q", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher kept running after its project was deleted")
	}
	time.Sleep(300 * time.Millisecond)
	if keys, err := bus.ProjectKeys(ctx, r, b.Project()); err != nil || len(keys) != 0 {
		t.Fatalf("keys after delete = %v %v, want none", keys, err)
	}
}
