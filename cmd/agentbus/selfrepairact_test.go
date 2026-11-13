package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

// actWorld is self-repair in on mode against a disposable broker: coder's
// process (102) is gone; the relaunch, the snapshots the proof reads and
// the desktop are fakes.
type actWorld struct {
	t        *testing.T
	b        *bus.Bus
	c        *selfRepairChore
	now      time.Time
	snaps    map[string]bus.AgentSnapshot
	mu       sync.Mutex
	relaunch []string // roles relaunched, with their session
	desktop  []string
	onLaunch func(role string, fence func(string) error) error
}

func newActWorld(t *testing.T) *actWorld {
	t.Helper()
	b := notifyBus(t) // holds the lease as "tok"
	ctx := context.Background()
	for _, a := range []string{"master", "coder", "foureyes"} {
		if _, err := b.Status(ctx, a, "idle", "", bus.AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	w := &actWorld{t: t, b: b, now: srT0}
	c, _ := srChore(map[int]uint64{101: 99, 103: 99, 202: 77})
	c.b, c.mode = b, selfrepair.ModeOn
	c.read = func(context.Context) (selfRepairReads, error) {
		r := srReads(w.now)
		past, err := b.SelfRepairPast(ctx)
		r.Past = past
		return r, err
	}
	// The proof reads coder's snapshot as the relaunch left it.
	w.snaps = srReads(srT0).Snaps
	c.agents = func(context.Context) (map[string]bus.AgentSnapshot, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		out := map[string]bus.AgentSnapshot{}
		for k, v := range w.snaps {
			out[k] = v
		}
		return out, nil
	}
	c.relaunch = func(_ context.Context, role, session string, fence func(string) error) error {
		w.mu.Lock()
		w.relaunch = append(w.relaunch, role+" "+session)
		w.mu.Unlock()
		if w.onLaunch != nil {
			return w.onLaunch(role, fence)
		}
		if err := fence(role); err != nil {
			return err
		}
		w.mu.Lock()
		s := w.snaps[role]
		s.PID, s.PIDStart, s.Pane = 202, 77, "w1:p9" // a new process in a new pane
		w.snaps[role] = s
		w.mu.Unlock()
		return nil
	}
	c.notify = func(_ context.Context, title, body string) error {
		w.desktop = append(w.desktop, title+": "+body)
		return nil
	}
	c.proofTries, c.sleep = 3, func(time.Duration) {}
	w.c = c
	return w
}

// passes runs n passes 20 s apart (three reads and the grace, then acts).
func (w *actWorld) passes(n int) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		if err := w.c.pass(context.Background(), "tok"); err != nil {
			w.t.Fatal(err)
		}
		w.now = w.now.Add(20 * time.Second)
	}
}

func (w *actWorld) toMaster() []string {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 200)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == "master" && e.Type == bus.CmdDirective {
			out = append(out, e.Message)
		}
	}
	return out
}

func (w *actWorld) ledger() []string {
	w.t.Helper()
	log, _ := w.b.SwapLog(context.Background(), "coder", 0)
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Event)
	}
	return out
}

// R1 (self-repair spec 4.1, step 7b): a dead agent past its grace is
// relaunched, resuming its proved session, under its fenced intent; the
// proof (a new pid alive, a new pane, its own client and provider) ends it
// relaunched; master is told once.
func TestSelfRepairRelaunches(t *testing.T) {
	w := newActWorld(t)
	w.passes(4)
	if len(w.relaunch) != 1 || w.relaunch[0] != "coder "+srSession {
		t.Fatalf("relaunched %q", w.relaunch)
	}
	if got := strings.Join(w.ledger(), " "); got != "begin relaunched" {
		t.Fatalf("ledger %q", got)
	}
	if _, held, _ := w.b.SwapIntentOf(context.Background(), "coder"); held {
		t.Fatal("the intent outlived the repair")
	}
	msgs := w.toMaster()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "self-repair: coder relaunched (pid 102 -> 202, pane w1:p102 -> w1:p9, resumed 58b5f18b)") {
		t.Fatalf("master told %q", msgs)
	}
	if past, _ := w.b.SelfRepairPast(context.Background()); len(past["coder"].Attempts) != 1 {
		t.Fatalf("attempts %+v", past["coder"])
	}
	if len(w.desktop) != 0 {
		t.Fatalf("a success rang the desktop: %q", w.desktop)
	}
}

// No proof within the wait (spec 4.1 Failure): failed, one escalation to
// master and one desktop ring, and no retry for that death.
func TestSelfRepairFailedProof(t *testing.T) {
	w := newActWorld(t)
	w.onLaunch = func(string, func(string) error) error { return nil } // nothing comes up
	w.passes(6)
	if len(w.relaunch) != 1 {
		t.Fatalf("retried: %q", w.relaunch)
	}
	if got := strings.Join(w.ledger(), " "); got != "begin failed" {
		t.Fatalf("ledger %q", got)
	}
	msgs := w.toMaster()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "self-repair: coder relaunch failed its proof") || !strings.Contains(msgs[0], "agent-launch coder demo --resume "+srSession) {
		t.Fatalf("master told %q", msgs)
	}
	if len(w.desktop) != 1 || !strings.Contains(w.desktop[0], "coder") {
		t.Fatalf("desktop %q", w.desktop)
	}
	if out, _ := w.c.check(context.Background()); out.Status != health.Fail {
		t.Fatalf("check %+v", out)
	}
}

// A relaunch that errs (its tab never settled, herdr refused) is a failed
// attempt too: escalated, never retried for that death.
func TestSelfRepairRelaunchError(t *testing.T) {
	w := newActWorld(t)
	w.onLaunch = func(string, func(string) error) error { return errors.New("its tab coder still reads busy") }
	w.passes(6)
	if len(w.relaunch) != 1 || strings.Join(w.ledger(), " ") != "begin failed" {
		t.Fatalf("relaunched %q, ledger %q", w.relaunch, w.ledger())
	}
	if msgs := w.toMaster(); len(msgs) != 1 || !strings.Contains(msgs[0], "its tab coder still reads busy") {
		t.Fatalf("master told %q", msgs)
	}
}

// The fence (spec 4.1): the lease lost between the intent and the split,
// the relaunch stops with nothing done; the finish cannot be written
// either, so the intent waits out its TTL.
func TestSelfRepairFenceStopsTheSplit(t *testing.T) {
	w := newActWorld(t)
	split := false
	w.onLaunch = func(role string, fence func(string) error) error {
		if err := w.b.ReleaseDispatch(context.Background(), "tok"); err != nil {
			t.Fatal(err)
		}
		if err := fence(role); err == nil {
			split = true
		}
		return errors.New("not relaunched")
	}
	for i := 0; i < 4; i++ {
		_ = w.c.pass(context.Background(), "tok")
		w.now = w.now.Add(20 * time.Second)
	}
	if split {
		t.Fatal("the fence let a split through without the lease")
	}
}

// A spent day (spec 4.5): a crash loop, marked once, escalated once (master
// and the desktop), nothing relaunched.
func TestSelfRepairCrashLoop(t *testing.T) {
	w := newActWorld(t)
	ctx := context.Background()
	for i, n := range []string{"a", "b", "c"} {
		if err := w.b.BeginSelfRepair(ctx, "tok", "coder", n, bus.SwapEvent{By: "dispatch", Episode: "9:" + n}); err != nil {
			t.Fatal(i, err)
		}
		if err := w.b.FinishSelfRepair(ctx, "tok", n, bus.SwapEvent{Role: "coder", Event: bus.SwapRelaunched, By: "dispatch", Episode: "9:" + n}); err != nil {
			t.Fatal(err)
		}
	}
	w.c.every = time.Second // spacing aside: the day's count decides
	w.passes(6)
	if len(w.relaunch) != 0 {
		t.Fatalf("relaunched in a crash loop: %q", w.relaunch)
	}
	if past, _ := w.b.SelfRepairPast(ctx); !past["coder"].CrashLoop {
		t.Fatal("no crash-loop mark")
	}
	if msgs := w.toMaster(); len(msgs) != 1 || !strings.Contains(msgs[0], "agentbus self-repair reset coder") {
		t.Fatalf("master told %q", msgs)
	}
	if len(w.desktop) != 1 {
		t.Fatalf("desktop %q", w.desktop)
	}
}

// Reported, never acted on (spec 4.1, 4.2): an optional role's missing tab,
// once per death.
func TestSelfRepairReports(t *testing.T) {
	w := newActWorld(t)
	w.c.herdr = func(map[string]bus.AgentSnapshot) selfRepairHerdr {
		return selfRepairHerdr{Up: true, Open: true, Judge: func(relaunch.Tab, bool) relaunch.Verdict { return relaunch.VerdictMissing }}
	}
	read := w.c.read
	w.c.read = func(ctx context.Context) (selfRepairReads, error) {
		r, err := read(ctx)
		r.Required["coder"] = false
		return r, err
	}
	w.passes(6)
	if len(w.relaunch) != 0 {
		t.Fatalf("relaunched %q", w.relaunch)
	}
	if msgs := w.toMaster(); len(msgs) != 1 || !strings.Contains(msgs[0], "self-repair: coder not relaunched: its tab missing") {
		t.Fatalf("master told %q", msgs)
	}
	if len(w.desktop) != 0 {
		t.Fatalf("a report rang the desktop: %q", w.desktop)
	}
}

// Shadow still only records: never a relaunch, never a message.
func TestSelfRepairShadowActsOnNothing(t *testing.T) {
	w := newActWorld(t)
	w.c.mode = selfrepair.ModeShadow
	w.passes(6)
	if len(w.relaunch) != 0 || len(w.toMaster()) != 0 || strings.Join(w.ledger(), " ") != "would-relaunch" {
		t.Fatalf("relaunched %q, master %q, ledger %q", w.relaunch, w.toMaster(), w.ledger())
	}
}

// The proof decides (spec 4.1): a relaunch that errs after the new agent
// came up (its old pane not closed) is relaunched, not failed.
func TestSelfRepairProofDecides(t *testing.T) {
	w := newActWorld(t)
	w.onLaunch = func(role string, fence func(string) error) error {
		w.mu.Lock()
		s := w.snaps[role]
		s.PID, s.PIDStart, s.Pane = 202, 77, "w1:p9"
		w.snaps[role] = s
		w.mu.Unlock()
		return errors.New("herdr pane close w1:p102: refused")
	}
	w.passes(4)
	if got := strings.Join(w.ledger(), " "); got != "begin relaunched" {
		t.Fatalf("ledger %q", got)
	}
}

// Another relauncher took the role between the read and the begin (an
// intent the facts did not see): nothing is relaunched, nothing failed.
func TestSelfRepairNeverNextToAnotherIntent(t *testing.T) {
	w := newActWorld(t)
	if err := w.b.BeginSwap(context.Background(), "coder", bus.SwapIntent{Kind: bus.SwapKindReload, By: "sentinel"}); err != nil {
		t.Fatal(err)
	}
	w.passes(4) // srReads lists no intent: Decide relaunches, the begin is refused
	if len(w.relaunch) != 0 {
		t.Fatalf("relaunched next to a reload: %q", w.relaunch)
	}
	if got := strings.Join(w.ledger(), " "); got != "begin" { // the reload's own begin only
		t.Fatalf("ledger %q", got)
	}
}

// The proof is reload's (spec 4.1): a new pid is not enough; it must be
// alive by its start time, in a new pane, on the role's own client and
// provider. Each short of it fails the proof.
func TestSelfRepairProofNeedsAllOfIt(t *testing.T) {
	for name, set := range map[string]func(*bus.AgentSnapshot){
		"the old pane":      func(s *bus.AgentSnapshot) { s.PID, s.PIDStart = 202, 77 },
		"another provider":  func(s *bus.AgentSnapshot) { s.PID, s.PIDStart, s.Pane, s.Provider = 202, 77, "w1:p9", "openai" },
		"not alive":         func(s *bus.AgentSnapshot) { s.PID, s.PIDStart, s.Pane = 303, 1, "w1:p9" },
		"its start differs": func(s *bus.AgentSnapshot) { s.PID, s.PIDStart, s.Pane = 202, 1, "w1:p9" },
	} {
		t.Run(name, func(t *testing.T) {
			w := newActWorld(t)
			w.onLaunch = func(role string, _ func(string) error) error {
				w.mu.Lock()
				s := w.snaps[role]
				set(&s)
				w.snaps[role] = s
				w.mu.Unlock()
				return nil
			}
			w.passes(4)
			if got := strings.Join(w.ledger(), " "); got != "begin failed" {
				t.Fatalf("ledger %q", got)
			}
		})
	}
}
