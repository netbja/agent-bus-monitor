package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
			// Not done at the first check: no begin, nothing in the ledger.
			if log, _ := w.b.SwapLog(ctx, "security", 0); at == 1 && len(log) != 0 {
				t.Errorf("%s at check 1 wrote the ledger: %+v", name, log)
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
	if r := popEnd(ctx, w.b, d, "tok", "foureyes", snaps["foureyes"].TS); r.Ended || !strings.Contains(r.Wait, "boot role") || w.closed() {
		t.Fatalf("a boot role: %+v closed=%v", r, w.closed())
	}
	w, d, _, _, ep = popEndWorld(t)
	d.ProcStart = func(int) (uint64, error) { return 0, errors.New("permission denied") }
	if r := popEnd(ctx, w.b, d, "tok", "security", ep); r.Ended || r.Wait == "" || w.closed() {
		t.Fatalf("an unreadable process: %+v closed=%v", r, w.closed())
	}
}

// The lease lost right before the close (another dispatcher took it): the
// intent is no longer held, nothing is closed.
func TestPopEndStopsWhenTheLeaseGoes(t *testing.T) {
	w, d, rt, _, ep := popEndWorld(t)
	ctx := context.Background()
	rt.onInspect = func(n int) {
		if n == 3 {
			w.r.Set(ctx, bus.DispatchKey(w.b.Project()), "other", time.Minute)
		}
	}
	r := popEnd(ctx, w.b, d, "tok", "security", ep)
	if r.Ended || r.Wait == "" || w.closed() || !w.registered(t, "security") {
		t.Fatalf("popEnd %+v closed=%v", r, w.closed())
	}
}

// foureyes P3: the pane closed but its process still runs past PIDGone: a
// failure, master told, the registration kept.
func TestPopEndFailsWhenThePidOutlivesTheClose(t *testing.T) {
	w, d, _, _, ep := popEndWorld(t)
	d.ClosePane = func(context.Context, string) error { return nil } // closes nothing: pid 6161 lives on
	d.PIDGone = 50 * time.Millisecond
	r := popEnd(context.Background(), w.b, d, "tok", "security", ep)
	if r.Ended || !strings.Contains(r.Failed, "still running") || !w.registered(t, "security") {
		t.Fatalf("popEnd %+v", r)
	}
}

// A close that fails (a herdr that answered an error or hit its deadline):
// a failure, nothing forgotten.
func TestPopEndFailsWhenTheCloseFails(t *testing.T) {
	w, d, _, _, ep := popEndWorld(t)
	d.ClosePane = func(context.Context, string) error { return context.DeadlineExceeded }
	r := popEnd(context.Background(), w.b, d, "tok", "security", ep)
	if r.Ended || !strings.Contains(r.Failed, "deadline") || !w.registered(t, "security") {
		t.Fatalf("popEnd %+v", r)
	}
}

// reviewer-codex: the service's close goes to herdr with a deadline, so a
// herdr that hangs never holds the dispatcher's pass.
func TestServiceClosePaneHasADeadline(t *testing.T) {
	dir := t.TempDir()
	hang := filepath.Join(dir, "herdr")
	if err := os.WriteFile(hang, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		return map[string]string{"HERDR_SESSION": "s", "HERDR_BIN_PATH": hang}[k]
	}
	closePane, err := serviceClosePane(env, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := closePane(context.Background(), "p1"); err == nil {
		t.Fatal("a hung herdr closed the pane")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the close outlived its deadline: %v", d)
	}
	if _, err := serviceClosePane(func(string) string { return "" }, time.Second); err == nil {
		t.Fatal("a close with no herdr session")
	}
}
