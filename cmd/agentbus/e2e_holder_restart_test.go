package main

// The holder's endurance test (holder hardening §5), beside the release
// test, on its fixtures and under the same gate. It pins today's recovery
// after a holder crash:
//
//  1. a team of two fake Claude Codes (master, coder), each woken once;
//  2. the holder process crashes: its children killed, its server closed,
//     then a new server on the same socket (the unit's restart);
//  3. health holder reads holder_restarted naming both, with self-repair's
//     queue (H3);
//  4. self-repair relaunches both, each resumed on its recorded session,
//     master first, each once;
//  5. each is woken again and drains; health holder reads ok;
//  6. the holder crashes and never comes back: health holder fails, placed
//     in For you with the command that starts it (H3).
//
// What it cannot prove (spec §5): systemd's or launchd's own behaviour
// (Restart=, the start limit, KillMode; the manager is a fake), a real
// client's resume (the fake records its --resume), a real reboot, and slow
// leaks over hours. Self-repair's grace and its pause between relaunches
// pass on a shifted clock: its decisions are the production ones, its
// waiting is not.

import (
	"context"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/service"
)

func TestHolderRestartEndToEnd(t *testing.T) {
	w := e2eUp(t)
	ctx := context.Background()
	b := w.b
	if err := b.SetBudget(ctx, "anthropic", bus.BudgetSnapshot{Provider: "anthropic", ObservedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	w.startDispatch(t)
	roles := []string{"master", "coder"}

	// health holder as the dispatch service runs it, on this world's units
	// (the fake manager up installed it with), sockets and copies.
	paths := service.Paths{DataHome: os.Getenv("XDG_DATA_HOME")}
	check := holderCheck(b, func() (health.HolderEvidence, error) {
		return holderEvidence(w.hm, paths, w.project, w.raw, realHolderProbe(os.Getenv)), nil
	}, os.Getenv)
	var out health.Outcome
	reads := func(st health.Status, want ...string) bool {
		var err error
		if out, err = check(ctx); err != nil || out.Status != st {
			return false
		}
		for _, s := range want {
			if !strings.Contains(out.Detail, s) {
				return false
			}
		}
		return true
	}
	if !until(10*time.Second, func() bool { return reads(health.OK, "answers") }) {
		t.Fatalf("before the crash: %+v", out)
	}

	// 1. Each woken once.
	for _, role := range roles {
		w.wake(t, role, "before the crash")
	}

	// 2. The crash: every program the holder held dies with it, then the
	// unit starts a new, empty holder on the same socket.
	crash := func() map[string]int {
		pids := map[string]int{}
		for _, role := range roles {
			_, tm := w.screen(t, role)
			if tm.PID <= 0 {
				t.Fatalf("%s has no live terminal before the crash", role)
			}
			pids[role] = tm.PID
			if err := syscall.Kill(tm.PID, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
		}
		w.stop()
		return pids
	}
	old := crash()
	time.Sleep(1100 * time.Millisecond) // the new socket's age, on its own clock, after every proof
	w.serve(t)

	// 3. holder_restarted names both, and the queue self-repair works
	// through (one relaunch every 2m, master first).
	if !until(10*time.Second, func() bool {
		return reads(health.Warn, "2 agent(s) lost: coder, master", "2 waiting for self-repair", "one relaunch every 2m0s, master first")
	}) {
		t.Fatalf("after the restart: %+v", out)
	}

	// 4. Self-repair relaunches both, resumed, master first, each once.
	w.killed.Store(true)
	for _, role := range roles {
		role := role
		var sc string
		if !until(60*time.Second, func() bool {
			sc, _ = w.screen(t, role)
			return strings.Contains(sc, "resumed "+w.session[role])
		}) {
			evs, _ := b.SwapLog(ctx, role, 20)
			t.Fatalf("%s was not relaunched on its session %s (swap ledger %+v):\n%s", role, w.session[role], evs, sc)
		}
		if !until(10*time.Second, func() bool {
			_, tm := w.screen(t, role)
			snaps, _ := b.Agents(ctx)
			return snaps[role].PID == tm.PID && tm.PID != old[role]
		}) {
			t.Fatalf("the relaunched %s never proved its process", role)
		}
	}
	// Each relaunch passed its proof (a new process in a new pane): a pane id
	// the restarted holder gave again would fail it, after 60 s.
	for _, role := range roles {
		role := role
		var evs []bus.SwapEvent
		if !until(20*time.Second, func() bool {
			evs, _ = b.SwapLog(ctx, role, 20)
			return slices.ContainsFunc(evs, func(e bus.SwapEvent) bool { return e.Event == bus.SwapRelaunched })
		}) || slices.ContainsFunc(evs, func(e bus.SwapEvent) bool { return e.Event == bus.SwapFailed }) {
			t.Fatalf("%s's relaunch did not pass its proof: swap ledger %+v", role, evs)
		}
	}
	w.mu.Lock()
	resumed := slices.Clone(w.resumed)
	w.mu.Unlock()
	if !slices.Equal(resumed, roles) {
		t.Fatalf("relaunched %v, want %v (master first, each once)", resumed, roles)
	}

	// 5. Each woken again; the holder reads ok, nothing lost any more.
	for _, role := range roles {
		w.wake(t, role, "after the restart")
	}
	if !until(10*time.Second, func() bool { return reads(health.OK, "answers, 2 terminal(s)") }) {
		t.Fatalf("after the relaunches: %+v", out)
	}

	// 6. A holder that never comes back fails, and the fail is the person's.
	crash()
	if !until(10*time.Second, func() bool { return reads(health.Fail, "no holder answers") }) {
		t.Fatalf("a holder that never came back: %+v", out)
	}
	d, _ := health.ByID("holder")
	if d.Place(out.Status) != health.PlaceForYou || !strings.Contains(d.Command, "service start holder") {
		t.Fatalf("the fail is not the person's: place %q, command %q", d.Place(out.Status), d.Command)
	}
}
