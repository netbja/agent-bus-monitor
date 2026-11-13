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
	// The snapshots as the relaunch leaves them: the proof and the next
	// passes read them.
	w.snaps = srReads(srT0).Snaps
	c.read = func(context.Context) (selfRepairReads, error) {
		r := srReads(w.now)
		past, err := b.SelfRepairPast(ctx)
		r.Past = past
		w.mu.Lock()
		for k, v := range w.snaps {
			r.Snaps[k] = v
		}
		w.mu.Unlock()
		return r, err
	}
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

// only is master's messages holding s (the crash-loop tests seed relaunch
// entries of their own, which are owed messages too).
func (w *actWorld) only(s string) []string {
	var out []string
	for _, m := range w.toMaster() {
		if strings.Contains(m, s) {
			out = append(out, m)
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
	if len(msgs) != 1 || !strings.Contains(msgs[0], "self-repair: coder relaunched (pid 102 -> 202, pane w1:p102 -> w1:p9, resumed 58b5f18b, it was dead 1m0s).") {
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
	if old := w.only("relaunched (pid"); len(old) > 1 { // only the newest end of a role is owed, once
		t.Fatalf("older ends re-announced: %q", old)
	}
	if msgs := w.only("crash loop"); len(msgs) != 1 || !strings.Contains(msgs[0], "agentbus self-repair reset coder") {
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

// foureyes' probe on 7b: coder dead on its quota fallback (moonshot, a
// fresh reading) while its own provider is held. A relaunch would run its
// own profile onto the held provider: told to master, never relaunched.
func TestSelfRepairNeverOnAFallback(t *testing.T) {
	for _, held := range []bool{true, false} {
		w := newActWorld(t)
		w.mu.Lock()
		s := w.snaps["coder"]
		s.Client, s.Provider = "claude-code", "moonshot"
		w.snaps["coder"] = s
		w.mu.Unlock()
		read := w.c.read
		w.c.read = func(ctx context.Context) (selfRepairReads, error) {
			r, err := read(ctx)
			r.Budgets["moonshot"] = bus.BudgetSnapshot{Provider: "moonshot", ObservedAt: w.now.UnixMilli()}
			if held {
				r.Marks["hold:anthropic"] = "1799999999"
			}
			r.Fallbacks = map[string]bus.FallbackRecord{"coder": {OwnProfile: "opus", OwnProvider: "anthropic", Profile: "kimi", Provider: "moonshot"}}
			return r, err
		}
		w.passes(6)
		if len(w.relaunch) != 0 {
			t.Fatalf("held %v: relaunched %q", held, w.relaunch)
		}
		if msgs := w.only("coder not relaunched"); len(msgs) != 1 || !strings.Contains(msgs[0], "fallback profile kimi") {
			t.Fatalf("held %v: master told %q", held, w.toMaster())
		}
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

// A message to master that could not be written (the lease lost, the
// broker down) is sent at a later pass, from what is durable: the ledger's
// entry for a relaunch or a failure, the crash-loop mark for a crash loop
// (reviewer-codex on 7b). Once written, never again; the desktop rings once.
func TestSelfRepairMessagesAreReplayed(t *testing.T) {
	cases := map[string]struct {
		setup func(w *actWorld)
		want  string
		rings int
	}{
		"relaunched": {func(*actWorld) {}, "self-repair: coder relaunched (pid 102 -> 202, pane w1:p102 -> w1:p9", 0},
		"failed": {func(w *actWorld) {
			w.onLaunch = func(string, func(string) error) error { return nil }
		}, "self-repair: coder relaunch failed its proof", 1},
		"crash loop": {func(w *actWorld) {
			ctx := context.Background()
			for _, n := range []string{"a", "b", "c"} {
				w.b.BeginSelfRepair(ctx, "tok", "coder", n, bus.SwapEvent{By: "dispatch", Episode: "9:" + n})
				w.b.FinishSelfRepair(ctx, "tok", n, bus.SwapEvent{Role: "coder", Event: bus.SwapRelaunched, By: "dispatch", Episode: "9:" + n})
			}
			w.c.every = time.Second
		}, "agentbus self-repair reset coder", 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newActWorld(t)
			tc.setup(w)
			failing := true
			send := w.c.sender()
			w.c.send = func(ctx context.Context, token, mark, episode, target, text string) (bool, error) {
				if failing {
					return false, errors.New("the broker is away")
				}
				return send(ctx, token, mark, episode, target, text)
			}
			for i := 0; i < 4; i++ { // the act, its send failing
				_ = w.c.pass(context.Background(), "tok")
				w.now = w.now.Add(20 * time.Second)
			}
			if got := w.toMaster(); len(got) != 0 {
				t.Fatalf("sent while failing: %q", got)
			}
			w.now = w.now.Add(25 * time.Hour) // a day and more: still owed (reviewer-codex)
			failing = false
			w.passes(3)
			msgs := w.only(tc.want)
			if len(msgs) != 1 {
				t.Fatalf("master told %q", msgs)
			}
			if len(w.desktop) != tc.rings {
				t.Fatalf("desktop %q", w.desktop)
			}
			if owed, _ := w.b.SelfRepairOwed(context.Background()); len(owed) != 0 {
				t.Fatalf("still owed after its delivery: %v", owed)
			}
		})
	}
}

// Master being swapped holds a message back: it is not counted written,
// and arrives once master can take it.
func TestSelfRepairMessageWaitsOnASwappedMaster(t *testing.T) {
	w := newActWorld(t)
	ctx := context.Background()
	if err := w.b.BeginSwap(ctx, "master", bus.SwapIntent{ToProfile: "opus", By: "person"}); err != nil {
		t.Fatal(err)
	}
	w.passes(5)
	if got := w.toMaster(); len(got) != 0 {
		t.Fatalf("sent to a master being swapped: %q", got)
	}
	if owed, _ := w.b.SelfRepairOwed(ctx); len(owed) != 1 {
		t.Fatalf("owed %v", owed)
	}
	if err := w.b.EndSwap(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	w.passes(1)
	if got := w.only("relaunched (pid"); len(got) != 1 {
		t.Fatalf("after the swap: %q", w.toMaster())
	}
}

// Two ends of one role owed at once (reviewer-codex on 7b): the first still
// waits (master being swapped) when the second is recorded; both reach
// master once it can take them, none replacing the other.
func TestSelfRepairTwoEndsOwed(t *testing.T) {
	w := newActWorld(t)
	ctx := context.Background()
	if err := w.b.BeginSwap(ctx, "master", bus.SwapIntent{ToProfile: "opus", By: "person"}); err != nil {
		t.Fatal(err)
	}
	for _, ep := range []string{"102:99", "202:77"} {
		n := "n" + ep[:3]
		if err := w.b.BeginSelfRepair(ctx, "tok", "coder", n, bus.SwapEvent{By: "dispatch", Episode: ep}); err != nil {
			t.Fatal(err)
		}
		raw := `{"mark":"selfrepair-done:coder","episode":"` + ep + `","text":"self-repair: coder relaunched (` + ep + `)."}`
		if err := w.b.FinishSelfRepairOwing(ctx, "tok", n, bus.SwapEvent{Role: "coder", Event: bus.SwapRelaunched, By: "dispatch", Episode: ep}, raw); err != nil {
			t.Fatal(err)
		}
		if err := w.c.deliver(ctx, "tok", "master"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.toMaster(); len(got) != 0 {
		t.Fatalf("sent during the swap: %q", got)
	}
	if err := w.b.EndSwap(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	if err := w.c.deliver(ctx, "tok", "master"); err != nil {
		t.Fatal(err)
	}
	got := w.toMaster()
	if len(got) != 2 || !strings.Contains(got[0], "(102:99)") || !strings.Contains(got[1], "(202:77)") {
		t.Fatalf("master told %q", got)
	}
	if owed, _ := w.b.SelfRepairOwed(ctx); len(owed) != 0 {
		t.Fatalf("still owed %+v", owed)
	}
}

// A failure still owed (master swapped), a reset, then a success for the
// same death (reviewer-codex on 7b, round 4): two ends, two messages, in
// order, none replacing the other.
func TestSelfRepairFailResetSucceedSameDeath(t *testing.T) {
	w := newActWorld(t)
	ctx := context.Background()
	if err := w.b.BeginSwap(ctx, "master", bus.SwapIntent{ToProfile: "opus", By: "person"}); err != nil {
		t.Fatal(err)
	}
	w.onLaunch = func(string, func(string) error) error { return nil } // no proof
	w.passes(5)
	if got := strings.Join(w.ledger(), " "); got != "begin failed" {
		t.Fatalf("ledger %q", got)
	}
	if _, err := w.b.ResetSelfRepair(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	w.onLaunch = nil             // the default: a new process comes up
	w.now = w.now.Add(time.Hour) // past the budget's spacing
	w.passes(2)
	if got := strings.Join(w.ledger(), " "); got != "begin failed begin relaunched" {
		t.Fatalf("ledger %q", got)
	}
	if got := w.toMaster(); len(got) != 0 {
		t.Fatalf("sent during the swap: %q", got)
	}
	if err := w.b.EndSwap(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	w.passes(1)
	got := w.toMaster()
	if len(got) != 2 || !strings.Contains(got[0], "relaunch failed") || !strings.Contains(got[1], "relaunched (pid 102 -> 202") {
		t.Fatalf("master told %q", got)
	}
}

// Step 8, R3: while relaunches are held for an unreachable provider, master
// is told once per episode (the provider and its last good reading); a
// later reading that goes stale again is a new episode. Nothing relaunched.
func TestSelfRepairNetworkHoldTold(t *testing.T) {
	w := newActWorld(t)
	stale := srT0.Add(-30 * time.Minute)
	reading := stale
	read := w.c.read
	w.c.read = func(ctx context.Context) (selfRepairReads, error) {
		r, err := read(ctx)
		r.Budgets["anthropic"] = bus.BudgetSnapshot{Provider: "anthropic", ObservedAt: reading.UnixMilli()}
		return r, err
	}
	w.passes(6)
	msgs := w.only("holding relaunches")
	if len(w.relaunch) != 0 || len(msgs) != 1 || !strings.Contains(msgs[0], "anthropic unreachable since "+stale.Local().Format("15:04")) ||
		!strings.Contains(msgs[0], "coder") {
		t.Fatalf("relaunched %q, master %q", w.relaunch, w.toMaster())
	}
	reading = srT0.Add(-20 * time.Minute) // a reading came, then went stale again: another episode
	w.passes(2)
	if msgs := w.only("holding relaunches"); len(msgs) != 2 {
		t.Fatalf("a new episode not told: %q", msgs)
	}
}

// Step 8, R4: started with the workspace closed, nothing is relaunched and
// master is told once to run agentbus up --resume; open, then closed again,
// is told again.
func TestSelfRepairWorkspaceClosedTold(t *testing.T) {
	w := newActWorld(t)
	open := false
	w.c.herdr = func(map[string]bus.AgentSnapshot) selfRepairHerdr {
		return selfRepairHerdr{Up: true, Open: open, Judge: func(relaunch.Tab, bool) relaunch.Verdict {
			return relaunch.VerdictBusy // open, the tab is waited on: nothing relaunched in this test
		}}
	}
	w.passes(6)
	msgs := w.only("workspace")
	if len(w.relaunch) != 0 || len(msgs) != 1 || !strings.Contains(msgs[0], "agentbus up demo --resume") {
		t.Fatalf("relaunched %q, master %q", w.relaunch, w.toMaster())
	}
	open = true
	w.passes(1)
	open = false
	w.passes(2)
	if msgs := w.only("workspace"); len(msgs) != 2 {
		t.Fatalf("closed again, not told: %q", msgs)
	}
}
