package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func suspendNow(t *testing.T, b *bus.Bus) string {
	t.Helper()
	_, raw, _, err := b.Suspend(context.Background(), bus.SuspendSpec{By: "charlie", Notify: "master", Reason: "reboot"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A pass that read the project before the suspend was written, and reaches
// its reservation after it, reserves nothing and types nothing: the
// reservation refuses under a pause in its own script (spec §3.1).
func TestRunPassReadBeforeTheSuspendReservesNothing(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PauseKey(b.Project())) })
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	written := make(chan struct{})
	rt := newFakeRT(func(_ string, n int) Inspection {
		if n == 2 { // prompt's re-check: after the pass's pause check, before the reservation
			suspendNow(t, b)
			close(written)
		}
		return readyIn()
	})
	start(t, b, rt, fastConfig())
	select {
	case <-written:
	case <-time.After(3 * time.Second):
		t.Fatal("the pass never reached its re-check")
	}
	time.Sleep(400 * time.Millisecond)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("%d prompts typed into a suspended project", n)
	}
	if w, ok := wakeOf(t, b, "coder"); ok && (w.State == "prompting" || w.State == "prompted") {
		t.Fatalf("wake record %+v written under the suspend", w)
	}
}

// A reservation written before the suspend is the one race left (spec §3.1):
// the prompt the pass then types is visible as a prompting, then prompted,
// record, which the suspend's quiet rule waits out. This pins what it sees.
func TestRunReservationBeforeTheSuspendStaysVisible(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PauseKey(b.Project())) })
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	seen := make(chan string, 1)
	withHooks(t, hooks{afterReserve: func() {
		suspendNow(t, b)
		w, _, _ := b.Wake(context.Background(), "coder")
		seen <- w.State
	}})
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	select {
	case st := <-seen:
		if st != "prompting" {
			t.Fatalf("record at the suspend = %q, want prompting", st)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reservation")
	}
	waitFor(t, 2*time.Second, "the prompted record", func() bool {
		w, ok := wakeOf(t, b, "coder")
		return ok && w.State == "prompted"
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(rt.Prompts()); n != 1 {
		t.Fatalf("prompts = %d, want exactly the one reserved before the suspend", n)
	}
}

// A suspend is never ended by the dispatcher, however due its Until: nothing
// is woken across many passes, and the record stays.
func TestRunSuspendHoldsHoweverDue(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PauseKey(b.Project())) })
	register(t, b, "coder", "p-coder")
	register(t, b, "master", "p-master")
	raw := suspendNow(t, b)
	var rec map[string]any
	_ = json.Unmarshal([]byte(raw), &rec)
	rec["until"] = 1
	past, _ := json.Marshal(rec)
	r.Set(ctx, bus.PauseKey(b.Project()), past, 0)
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	time.Sleep(time.Second)
	if n := len(rt.Prompts()); n != 0 {
		t.Fatalf("%d prompts during a suspend", n)
	}
	if p, _ := b.Pause(ctx); p == nil || !p.Suspended() {
		t.Fatalf("suspend ended by the dispatcher: %+v", p)
	}
	if w, ok := wakeOf(t, b, "coder"); ok && w.Escalated != "" {
		t.Fatalf("escalated during a suspend: %+v", w)
	}
}
