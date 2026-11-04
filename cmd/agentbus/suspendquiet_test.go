package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const qNow = int64(1_800_000_000_000)

func readyObs(at int64) *bus.Observation {
	return &bus.Observation{State: "idle", At: at, Since: at, PID: 7}
}

func idleSnap(ts int64) bus.AgentSnapshot {
	return bus.AgentSnapshot{State: "idle", TS: ts, PID: 7, PIDStart: 70, Pane: "p"}
}

// One round of the predicate on a single agent, from a fresh state.
func judgeOne(t *testing.T, snap bus.AgentSnapshot, wake *bus.WakeRecord, obs *bus.Observation, gone bool, dispatchGoneAt int64) []busyAgent {
	t.Helper()
	f := quietFacts{Now: qNow, Agents: map[string]bus.AgentSnapshot{"coder": snap},
		Wakes: map[string]bus.WakeRecord{}, Obs: map[string]*bus.Observation{"coder": obs},
		Gone: map[string]bool{"coder": gone}, DispatchGoneAt: dispatchGoneAt, StaleAfter: (2 * time.Minute).Milliseconds()}
	if wake != nil {
		f.Wakes["coder"] = *wake
	}
	return newQuietState().judge(f)
}

func TestSuspendQuietTable(t *testing.T) {
	recent := qNow - 60_000
	for _, c := range []struct {
		name     string
		snap     bus.AgentSnapshot
		wake     *bus.WakeRecord
		obs      *bus.Observation
		gone     bool
		goneAt   int64
		wantBusy string // "" = quiet
	}{
		{name: "idle, pane ready", snap: idleSnap(recent), obs: readyObs(qNow)},
		{name: "declared working", snap: bus.AgentSnapshot{State: "working", TS: recent, PID: 7}, obs: readyObs(qNow), wantBusy: "working"},
		{name: "declared working, no inspection", snap: bus.AgentSnapshot{State: "working", TS: recent, PID: 7}, wantBusy: "working"},
		{name: "idle but its pane runs a turn", snap: idleSnap(recent), obs: &bus.Observation{State: "working", Busy: true, At: qNow, Since: qNow}, wantBusy: "pane busy"},
		{name: "dormant and working", snap: bus.AgentSnapshot{State: "working", TS: qNow - (25 * time.Hour).Milliseconds(), PID: 7}},
		{name: "process gone, left working", snap: bus.AgentSnapshot{State: "working", TS: recent, PID: 7}, gone: true},
		{name: "stale turn flip, pane ready long enough (foureyes)",
			snap: bus.AgentSnapshot{State: "working", Auto: bus.AutoTurn, TS: qNow - 10*60_000, PID: 7},
			obs:  &bus.Observation{State: "idle", At: qNow, Since: qNow - 5*60_000, PID: 7}},
		{name: "fresh turn flip", snap: bus.AgentSnapshot{State: "working", Auto: bus.AutoTurn, TS: recent, PID: 7},
			obs: &bus.Observation{State: "idle", At: qNow, Since: qNow - 10_000, PID: 7}, wantBusy: "working"},
		{name: "wake in flight, dispatcher alive", snap: idleSnap(recent), obs: readyObs(qNow),
			wake: &bus.WakeRecord{State: "prompting", At: qNow - time.Hour.Milliseconds()}, wantBusy: "wake in flight"},
		{name: "prompted, nothing seen since", snap: idleSnap(recent), obs: readyObs(qNow),
			wake: &bus.WakeRecord{State: "prompted", At: qNow - 1000}, wantBusy: "woken, turn not seen ending"},
		{name: "unobserved counts as sent", snap: idleSnap(recent), obs: readyObs(qNow),
			wake: &bus.WakeRecord{State: "unobserved", At: qNow - 1000}, wantBusy: "woken, turn not seen ending"},
		{name: "a deferred record typed nothing", snap: idleSnap(recent), obs: readyObs(qNow),
			wake: &bus.WakeRecord{State: "deferred", At: qNow - 1000}},
	} {
		got := judgeOne(t, c.snap, c.wake, c.obs, c.gone, c.goneAt)
		switch {
		case c.wantBusy == "" && len(got) != 0:
			t.Errorf("%s: busy %+v, want quiet", c.name, got)
		case c.wantBusy != "" && (len(got) != 1 || got[0].Why != c.wantBusy):
			t.Errorf("%s: %+v, want busy %q", c.name, got, c.wantBusy)
		}
	}
}

// A wake's boundary B is the suspend's own read, never the record's At
// (reviewer-codex): a non-working declaration later than At but not later
// than B answers nothing; one later than B does; two ready inspections at or
// after B + settle do.
func TestSuspendQuietWakeBoundary(t *testing.T) {
	s := newQuietState()
	f := func(now int64, snap bus.AgentSnapshot, wake bus.WakeRecord, obs *bus.Observation, goneAt int64) quietFacts {
		return quietFacts{Now: now, Agents: map[string]bus.AgentSnapshot{"coder": snap},
			Wakes: map[string]bus.WakeRecord{"coder": wake}, Obs: map[string]*bus.Observation{"coder": obs},
			Gone: map[string]bool{}, DispatchGoneAt: goneAt, StaleAfter: 120_000}
	}
	at := qNow - 30_000
	// Round 1: still prompting, the dispatcher alive and holding: busy, at any age.
	if b := s.judge(f(qNow, idleSnap(at+5_000), bus.WakeRecord{State: "prompting", At: at}, readyObs(qNow), 0)); len(b) != 1 {
		t.Fatalf("prompting: %+v", b)
	}
	// Round 2: the prompt was typed. B is this read, not At: the idle declared
	// after At but before B answers nothing.
	t2 := qNow + 2_000
	if b := s.judge(f(t2, idleSnap(at+5_000), bus.WakeRecord{State: "prompted", At: at}, readyObs(t2), 0)); len(b) != 1 {
		t.Fatalf("declaration before B answered the wake: %+v", b)
	}
	// Ready inspections before B + settle do not count either.
	t3 := t2 + 2_000
	if b := s.judge(f(t3, idleSnap(at+5_000), bus.WakeRecord{State: "prompted", At: at}, readyObs(t3), 0)); len(b) != 1 {
		t.Fatalf("inspection before B+settle counted: %+v", b)
	}
	// Two ready inspections at or after B + settle answer it.
	t4 := t2 + quietSettle.Milliseconds()
	if b := s.judge(f(t4, idleSnap(at+5_000), bus.WakeRecord{State: "prompted", At: at}, readyObs(t4), 0)); len(b) != 1 {
		t.Fatalf("one ready inspection was enough: %+v", b)
	}
	t5 := t4 + 2_000
	if b := s.judge(f(t5, idleSnap(at+5_000), bus.WakeRecord{State: "prompted", At: at}, readyObs(t5), 0)); len(b) != 0 {
		t.Fatalf("two ready inspections after B+settle: %+v", b)
	}

	// A declaration later than B answers at once.
	s = newQuietState()
	s.judge(f(qNow, idleSnap(at), bus.WakeRecord{State: "prompted", At: at}, nil, 0))
	if b := s.judge(f(qNow+2_000, idleSnap(qNow+1_000), bus.WakeRecord{State: "prompted", At: at}, nil, 0)); len(b) != 0 {
		t.Fatalf("a declaration after B: %+v", b)
	}
	// A working declaration later than B answers nothing.
	s = newQuietState()
	s.judge(f(qNow, idleSnap(at), bus.WakeRecord{State: "prompted", At: at}, nil, 0))
	work := bus.AgentSnapshot{State: "working", TS: qNow + 1_000, PID: 7}
	if b := s.judge(f(qNow+2_000, work, bus.WakeRecord{State: "prompted", At: at}, nil, 0)); len(b) != 1 {
		t.Fatalf("working after B: %+v", b)
	}

	// The dispatcher proved gone resolves a prompting record, with B at the proof.
	s = newQuietState()
	gone := qNow + 1_000
	if b := s.judge(f(qNow+2_000, idleSnap(at), bus.WakeRecord{State: "prompting", At: at}, readyObs(qNow+2_000), gone)); len(b) != 1 || b[0].Why != "woken, turn not seen ending" {
		t.Fatalf("prompting with the dispatcher gone: %+v", b)
	}
	s.judge(f(gone+quietSettle.Milliseconds(), idleSnap(at), bus.WakeRecord{State: "prompting", At: at}, readyObs(gone+quietSettle.Milliseconds()), gone))
	if b := s.judge(f(gone+quietSettle.Milliseconds()+2_000, idleSnap(at), bus.WakeRecord{State: "prompting", At: at},
		readyObs(gone+quietSettle.Milliseconds()+2_000), gone)); len(b) != 0 {
		t.Fatalf("prompting answered after the proof: %+v", b)
	}
}

// A busy inspection between two ready ones starts the count again.
func TestSuspendQuietReadyRoundsAreConsecutive(t *testing.T) {
	s := newQuietState()
	at := qNow - 30_000
	mk := func(now int64, obs *bus.Observation) quietFacts {
		return quietFacts{Now: now, Agents: map[string]bus.AgentSnapshot{"coder": idleSnap(at)},
			Wakes: map[string]bus.WakeRecord{"coder": {State: "prompted", At: at}}, Obs: map[string]*bus.Observation{"coder": obs},
			Gone: map[string]bool{}, StaleAfter: 120_000}
	}
	s.judge(mk(qNow, readyObs(qNow))) // B = qNow
	t1 := qNow + quietSettle.Milliseconds()
	s.judge(mk(t1, readyObs(t1)))
	busyObs := &bus.Observation{State: "working", Busy: true, At: t1 + 2_000, Since: t1 + 2_000}
	s.judge(mk(t1+2_000, busyObs))
	if b := s.judge(mk(t1+4_000, readyObs(t1+4_000))); len(b) != 1 {
		t.Fatalf("a ready inspection after a busy one answered the wake: %+v", b)
	}
	if b := s.judge(mk(t1+6_000, readyObs(t1+6_000))); len(b) != 0 {
		t.Fatalf("two ready inspections after: %+v", b)
	}
}
