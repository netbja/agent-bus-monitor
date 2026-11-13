package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The dispatcher's own end of a done pop (pop-retire-auto): the world of
// retire --forget's tests (newPopWorld), the dispatch lease held as "tok".
func popEndWorld(t *testing.T) (*swapWorld, swapDeps, *popRT, *func(), int64) {
	t.Helper()
	w, d, rt, afterClose := newPopWorld(t)
	if ok, err := w.b.AcquireDispatch(context.Background(), "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	snaps, _ := w.b.Agents(context.Background())
	return w, d, rt, afterClose, snaps["security"].TS
}

func TestPopEndEndsAFinishedPop(t *testing.T) {
	w, d, _, _, ep := popEndWorld(t)
	ctx := context.Background()
	r := popEnd(ctx, w.b, d, "tok", "security", ep)
	if !r.Ended || !strings.Contains(r.Detail, "pane p-sec closed") || !strings.Contains(r.Detail, "unregistered") {
		t.Fatalf("popEnd %+v", r)
	}
	if !w.closed() || w.registered(t, "security") {
		t.Fatalf("closed %v registered %v", w.closed(), w.registered(t, "security"))
	}
	log, _ := w.b.SwapLog(ctx, "security", 0)
	if len(log) < 2 || log[0].Event != bus.SwapRetire || log[0].Forgot != "true" || log[0].By != "dispatch" ||
		log[1].Event != bus.SwapBegin || log[1].Kind != bus.SwapKindRetire || log[1].By != "dispatch" {
		t.Fatalf("ledger %+v", log)
	}
	if ok, _ := w.b.Swapping(ctx, "security"); ok {
		t.Fatal("intent left behind")
	}
}

// Every check re-judges the whole predicate, as retire --forget's do: a
// directive or the focus that comes at any of the three checks stops it,
// nothing closed, the pop registered. Before the begin it is a wait with
// nothing written; after it, the ledger says why and it is still a wait (no
// pane was touched: the next pass judges again).
func TestPopEndReJudgesAtEachCheck(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"a directive", "the focus"} {
		for _, at := range []int{1, 2, 3} {
			w, d, rt, _, ep := popEndWorld(t)
			apply := func() {
				if name == "the focus" {
					rt.focused = true
					return
				}
				w.b.Cmd(ctx, "master", "security", "directive", "", "one more")
			}
			trigger := at - 1
			if name == "the focus" {
				trigger = at
			}
			if trigger == 0 {
				apply()
			} else {
				rt.onInspect = func(n int) {
					if n == trigger {
						apply()
					}
				}
			}
			r := popEnd(ctx, w.b, d, "tok", "security", ep)
			if r.Ended || r.Failed != "" || r.Wait == "" || w.closed() || !w.registered(t, "security") {
				t.Errorf("%s at check %d: %+v closed=%v", name, at, r, w.closed())
				continue
			}
			if ok, _ := w.b.Swapping(ctx, "security"); ok {
				t.Errorf("%s at check %d: intent left behind", name, at)
			}
		}
	}
}

// What arrives between the kill and the forget keeps the registration and
// the group: a failure, master told by the pass.
func TestPopEndKeepsWhatArrivesAfterTheKill(t *testing.T) {
	w, d, _, afterClose, ep := popEndWorld(t)
	ctx := context.Background()
	*afterClose = func() { w.b.Cmd(ctx, "master", "security", "directive", "", "late work") }
	r := popEnd(ctx, w.b, d, "tok", "security", ep)
	if r.Ended || !strings.Contains(r.Failed, "registration is kept") || !w.closed() || !w.registered(t, "security") {
		t.Fatalf("popEnd %+v closed=%v", r, w.closed())
	}
	log, _ := w.b.SwapLog(ctx, "security", 0)
	if log[0].Event != bus.SwapRetire || log[0].Forgot != "false" {
		t.Fatalf("ledger %+v", log[0])
	}
}

// A pop closed by hand is forgotten without a pane to close.
func TestPopEndForgetsAClosedPop(t *testing.T) {
	w, d, rt, _, _ := popEndWorld(t)
	w.closeByHand(t, rt)
	ep, _ := strconv.ParseInt(w.episode(t), 10, 64)
	r := popEnd(context.Background(), w.b, d, "tok", "security", ep)
	if !r.Ended || w.closed() || w.registered(t, "security") || !strings.Contains(r.Detail, "already closed") {
		t.Fatalf("popEnd %+v closed=%v", r, w.closed())
	}
}

// Without the lease nothing is written or closed; a running turn, a boot
// role and a pane whose process cannot be read are waited on.
func TestPopEndRefusals(t *testing.T) {
	ctx := context.Background()
	w, d, _, _, ep := popEndWorld(t)
	if r := popEnd(ctx, w.b, d, "other", "security", ep); r.Ended || w.closed() {
		t.Fatalf("no lease: %+v closed=%v", r, w.closed())
	}
	if log, _ := w.b.SwapLog(ctx, "security", 0); len(log) != 0 {
		t.Fatalf("no lease wrote the ledger %+v", log)
	}
	w, d, _, _, ep = popEndWorld(t)
	w.b.FencedPutObserved(ctx, "tok", map[string]bus.Observation{"security": {Busy: true, At: time.Now().UnixMilli()}})
	if r := popEnd(ctx, w.b, d, "tok", "security", ep); r.Wait == "" || !strings.Contains(r.Wait, "turn") || w.closed() {
		t.Fatalf("a running turn: %+v closed=%v", r, w.closed())
	}
	w, d, _, _, _ = popEndWorld(t)
	snaps, _ := w.b.Agents(ctx)
	if r := popEnd(ctx, w.b, d, "tok", "foureyes", snaps["foureyes"].TS); r.Ended || r.Wait == "" || w.closed() {
		t.Fatalf("a boot role: %+v closed=%v", r, w.closed())
	}
	w, d, _, _, ep = popEndWorld(t)
	d.ProcStart = func(int) (uint64, error) { return 0, errors.New("permission denied") }
	if r := popEnd(ctx, w.b, d, "tok", "security", ep); r.Ended || r.Wait == "" || w.closed() {
		t.Fatalf("an unreadable process: %+v closed=%v", r, w.closed())
	}
}
