package dispatch

import (
	"context"
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
