package dispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The dispatcher publishes what it sees in each verified pane, and only that:
// a working pane is busy, a pane whose foreground process is someone else is
// not observed, the declared state is untouched and nothing is typed.
func TestObservePassPublishesVerifiedPanes(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "dev", "p1")
	register(t, b, "doc", "p2")
	register(t, b, "other", "p3")
	rt := newFakeRT(func(pane string, _ int) Inspection {
		in := readyIn()
		switch pane {
		case "p1":
			in.State, in.Ready = "working", false
		case "p3":
			in.PIDs = []int{9999} // a recycled pane: not this agent
		}
		return in
	})
	cfg := fastConfig()
	cfg.Observe = 50 * time.Millisecond
	start(t, b, rt, cfg)

	var obs map[string]bus.Observation
	waitFor(t, 5*time.Second, "an observation of dev and doc", func() bool {
		var err error
		obs, err = b.Observed(ctx)
		return err == nil && len(obs) >= 2
	})
	if !obs["dev"].Busy || obs["dev"].State != "working" || obs["dev"].At == 0 {
		t.Errorf("dev = %+v, want busy working with a time", obs["dev"])
	}
	if o, ok := obs["doc"]; !ok || o.Busy {
		t.Errorf("doc = %+v (present %v), want observed not busy", o, ok)
	}
	if _, ok := obs["other"]; ok {
		t.Errorf("a pane failing the identity check was observed: %+v", obs["other"])
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snaps["dev"].State != "idle" {
		t.Errorf("declared state changed to %q", snaps["dev"].State)
	}
	if len(rt.Prompts()) != 0 {
		t.Errorf("observation typed into a pane: %+v", rt.Prompts())
	}
}

// Off by default: a dispatcher without Observe inspects no idle pane.
func TestObserveOffByDefault(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "dev", "p1")
	rt := newFakeRT(alwaysReady)
	r := start(t, b, rt, fastConfig())
	time.Sleep(200 * time.Millisecond)
	r.stop(t)
	if n := rt.Inspections("p1"); n != 0 {
		t.Errorf("inspected %d times with Observe off", n)
	}
	if obs, _ := b.Observed(context.Background()); len(obs) != 0 {
		t.Errorf("observed without Observe: %+v", obs)
	}
}

// A paused project is not observed: nothing to wake, so nothing to pay for.
func TestObserveSkipsAPausedProject(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "dev", "p1")
	if _, err := b.SetPause(ctx, "bernard", "master", "test", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.Observe = 20 * time.Millisecond
	r := start(t, b, rt, cfg)
	time.Sleep(300 * time.Millisecond)
	r.stop(t)
	if n := rt.Inspections("p1"); n != 0 {
		t.Errorf("a paused project was inspected %d times", n)
	}
}

// v0.15 §13: since stays while the state and the pid hold, and a failed
// inspection keeps the previous reading instead of dropping the pane.
func TestObserveCarriesTheEpisodeAndSurvivesAFailedLook(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	register(t, b, "master", "p1")
	var mode atomic.Int32 // 0 blocked, 1 inspection error, 2 idle
	rt := newFakeRT(func(string, int) Inspection {
		in := readyIn()
		switch mode.Load() {
		case 0:
			in.State, in.Ready = "blocked", false
		case 1:
			return Inspection{Err: errors.New("herdr timeout")}
		case 3:
			in.PIDs = []int{9999}
		}
		return in
	})
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	start(t, b, rt, cfg)
	var first bus.Observation
	waitFor(t, 5*time.Second, "a blocked observation", func() bool {
		obs, err := b.Observed(ctx)
		first = obs["master"]
		return err == nil && first.State == "blocked" && first.Since != 0
	})
	waitFor(t, 5*time.Second, "a later pass, same episode", func() bool {
		obs, _ := b.Observed(ctx)
		o := obs["master"]
		return o.At > first.At && o.Since == first.Since
	})
	mode.Store(1)
	time.Sleep(200 * time.Millisecond)
	obs, _ := b.Observed(ctx)
	if o, ok := obs["master"]; !ok || o.State != "blocked" || o.Since != first.Since {
		t.Fatalf("after a failed look = %+v (present %v), want the previous reading", o, ok)
	}
	mode.Store(3) // another process in the pane: the reading stands, with its pid
	time.Sleep(200 * time.Millisecond)
	obs, _ = b.Observed(ctx)
	if o, ok := obs["master"]; !ok || o.Since != first.Since || o.PID != testPID {
		t.Fatalf("after a foreign process = %+v (present %v), want the previous reading", o, ok)
	}
	mode.Store(2)
	waitFor(t, 5*time.Second, "a new episode", func() bool {
		obs, _ := b.Observed(ctx)
		o := obs["master"]
		return o.State == "idle" && o.Since > first.Since
	})
}
