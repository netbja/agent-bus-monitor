package dispatch

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var testPolicy = Policy{
	Retry:         60 * time.Second,
	EscalateAfter: 10 * time.Minute,
	MaxTries:      3,
	Supported:     map[string]bool{"claude": true, "kimi": true},
}

// owed is an obligation with one unread message and progress marks matching
// mark() below, so a record built from mark() sees no progress.
func owed() bus.Obligation {
	return bus.Obligation{GroupExists: true, Unread: 1, OldestID: "1000-0", HighID: "1000-0",
		LastDelivered: "900-0", PendingTotal: 2}
}

func snap() *bus.AgentSnapshot {
	return &bus.AgentSnapshot{State: "idle", Pane: "w1:p1", PID: 100, PIDStart: 5}
}

// ready is an inspection under which the registered agent may be prompted.
func ready() Inspection {
	return Inspection{State: "idle", Client: "claude", PIDs: []int{42, 100},
		Starts: map[int]uint64{42: 1, 100: 5}, Ready: true}
}

// mark is a wake record whose progress marks and identity match owed()/snap().
func mark(tries int, escalated string, at int64) *bus.WakeRecord {
	return &bus.WakeRecord{State: "prompted", At: at, Tries: tries, PID: 100, PIDStart: 5,
		Escalated: escalated, LastDelivered: "900-0", PendingTotal: 2}
}

func TestDecide(t *testing.T) {
	T := testPolicy.EscalateAfter
	type want struct {
		act   Action
		rule  int
		reset bool
		clear bool
	}
	cases := []struct {
		name string
		x    Input
		w    want
	}{
		{"rule1-no-obligation", Input{Obligation: bus.Obligation{GroupExists: true}, Snap: snap(), In: ready()},
			want{ActNone, 1, false, true}},
		{"stale-only-none", Input{Obligation: bus.Obligation{GroupExists: true, Stale: 4}, Snap: snap(), In: ready()},
			want{ActNone, 1, false, true}},
		{"rule2-receiver-armed", Input{Obligation: owed(), Armed: true, Snap: snap(), In: ready()},
			want{ActNone, 2, false, false}},
		{"rule3-already-escalated", Input{Obligation: owed(), Snap: snap(), In: ready(),
			Wake: mark(3, "true", 1), SinceWake: time.Hour, OldestAge: time.Hour},
			want{ActNone, 3, false, false}},
		{"escalation-pending-resends", Input{Obligation: owed(), Snap: snap(), In: ready(),
			Wake: mark(3, "pending", 1), SinceWake: time.Hour, OldestAge: time.Hour},
			want{ActEscalate, 3, false, false}},
		{"tries-cap-escalates", Input{Obligation: owed(), Snap: snap(), In: ready(),
			Wake: mark(3, "", 1), SinceWake: time.Hour},
			want{ActEscalate, 4, false, false}},
		{"burst-within-window-none", Input{Obligation: func() bus.Obligation {
			o := owed()
			o.Unread, o.HighID = 7, "5000-3" // new ids do not bypass the window
			return o
		}(), Snap: snap(), In: ready(), Wake: mark(1, "", 1), SinceWake: 10 * time.Second},
			want{ActNone, 5, false, false}},
		{"retry-window-elapsed-prompts", Input{Obligation: owed(), Snap: snap(), In: ready(),
			Wake: mark(1, "", 1), SinceWake: 61 * time.Second},
			want{ActPrompt, 9, false, false}},
		{"cleared-record-at-zero-no-window", Input{Obligation: owed(), Snap: snap(), In: ready(),
			Wake: mark(0, "", 0), SinceWake: 0},
			want{ActPrompt, 9, false, false}},
		{"rule6-no-snapshot-defers", Input{Obligation: owed(), In: ready()},
			want{ActDefer, 6, false, false}},
		{"rule6-unverified-old-binary-defers", Input{Obligation: owed(),
			Snap: &bus.AgentSnapshot{State: "idle", Pane: "w1:p1"}, In: ready()},
			want{ActDefer, 6, false, false}},
		{"rule6-no-snapshot-escalates-at-T", Input{Obligation: owed(), In: ready(), OldestAge: T + time.Second},
			want{ActEscalate, 6, false, false}},
		{"restart-same-pid-new-start-defers", Input{Obligation: owed(), Snap: snap(), In: func() Inspection {
			in := ready()
			in.Starts[100] = 6
			return in
		}()}, want{ActDefer, 6, false, false}},
		{"identity-change-resets-and-defers-if-unverified", Input{Obligation: owed(),
			Snap: &bus.AgentSnapshot{State: "idle", PID: 200, PIDStart: 9}, In: ready(),
			Wake: mark(3, "true", 1), SinceWake: 10 * time.Second},
			want{ActDefer, 6, true, false}},
		{"rule7-inspect-error", Input{Obligation: owed(), Snap: snap(), In: func() Inspection {
			in := ready()
			in.Err = errors.New("herdr down")
			return in
		}()}, want{ActDefer, 7, false, false}},
		{"rule7-pane-gone-escalates-at-T", Input{Obligation: owed(), Snap: snap(), OldestAge: T + time.Second,
			In: Inspection{Gone: true}}, want{ActEscalate, 7, false, false}},
		{"unsupported-client-never-prompts", Input{Obligation: owed(), Snap: snap(), In: func() Inspection {
			in := ready()
			in.Client = "codex"
			return in
		}()}, want{ActDefer, 7, false, false}},
		{"rule8-not-ready-defers", Input{Obligation: owed(), Snap: snap(), In: func() Inspection {
			in := ready()
			in.Ready = false
			return in
		}()}, want{ActDefer, 8, false, false}},
		{"kimi-done-but-busy-defers", Input{Obligation: owed(), Snap: snap(), In: func() Inspection {
			in := ready()
			in.Client, in.State, in.Busy = "kimi", "done", true
			return in
		}()}, want{ActDefer, 8, false, false}},
		{"focused-forever-escalates-once", Input{Obligation: owed(), Snap: snap(), OldestAge: T + time.Second,
			In: func() Inspection {
				in := ready()
				in.Focused = true
				return in
			}()}, want{ActEscalate, 8, false, false}},
		{"working-defers-past-T", Input{Obligation: owed(), Snap: snap(), OldestAge: 3 * T,
			In: func() Inspection {
				in := ready()
				in.State = "working"
				return in
			}()}, want{ActDefer, 8, false, false}},
		{"working-escalates-only-after-4T", Input{Obligation: owed(), Snap: snap(), OldestAge: 4*T + time.Second,
			In: func() Inspection {
				in := ready()
				in.State = "working"
				return in
			}()}, want{ActEscalate, 8, false, false}},
		{"blocked-escalates-at-T", Input{Obligation: owed(), Snap: snap(), OldestAge: T + time.Second,
			In: func() Inspection {
				in := ready()
				in.State = "blocked"
				return in
			}()}, want{ActEscalate, 8, false, false}},
		{"rule9-ready-prompts", Input{Obligation: owed(), Snap: snap(), In: ready()},
			want{ActPrompt, 9, false, false}},
		{"progress-resets-tries-prompts", Input{Obligation: func() bus.Obligation {
			o := owed()
			o.LastDelivered = "950-0"
			return o
		}(), Snap: snap(), In: ready(), Wake: mark(3, "", 1), SinceWake: 10 * time.Second},
			want{ActPrompt, 9, true, false}},
		{"pel-shrink-is-progress", Input{Obligation: func() bus.Obligation {
			o := owed()
			o.PendingTotal = 1
			return o
		}(), Snap: snap(), In: ready(), Wake: mark(3, "", 1), SinceWake: time.Hour},
			want{ActPrompt, 9, true, false}},
		{"growing-pel-is-not-progress", Input{Obligation: func() bus.Obligation {
			o := owed()
			o.PendingTotal = 5
			return o
		}(), Snap: snap(), In: ready(), Wake: mark(3, "", 1), SinceWake: time.Hour},
			want{ActEscalate, 4, false, false}},
		{"escalated-then-progress-can-escalate-again", Input{Obligation: func() bus.Obligation {
			o := owed()
			o.LastDelivered = "950-0"
			return o
		}(), Snap: snap(), OldestAge: T + time.Second, Wake: mark(3, "true", 1), SinceWake: time.Hour,
			In: func() Inspection {
				in := ready()
				in.Focused = true
				return in
			}()}, want{ActEscalate, 8, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(testPolicy, c.x)
			if d.Action != c.w.act || d.Rule != c.w.rule || d.ResetTries != c.w.reset || d.Clear != c.w.clear {
				t.Fatalf("Decide = %+v, want action=%v rule=%d reset=%v clear=%v",
					d, c.w.act, c.w.rule, c.w.reset, c.w.clear)
			}
			if d.Reason == "" || d.Reason != strings.ToLower(d.Reason) {
				t.Errorf("Reason %q must be a non-empty lowercase phrase", d.Reason)
			}
		})
	}
}

// A focused pane escalates once; the next tick, with the record now saying
// escalated=true and no progress, stays quiet (rule 3).
func TestFocusedForeverEscalatesOnce(t *testing.T) {
	in := ready()
	in.Focused = true
	x := Input{Obligation: owed(), Snap: snap(), In: in, OldestAge: 11 * time.Minute}
	if d := Decide(testPolicy, x); d.Action != ActEscalate {
		t.Fatalf("first = %+v, want escalate", d)
	}
	x.Wake = &bus.WakeRecord{State: "deferred", Escalated: "true", PID: 100, PIDStart: 5,
		LastDelivered: "900-0", PendingTotal: 2}
	if d := Decide(testPolicy, x); d.Action != ActNone || d.Rule != 3 {
		t.Fatalf("second = %+v, want none by rule 3", d)
	}
}

func TestProgressed(t *testing.T) {
	o := owed()
	cases := []struct {
		name string
		w    *bus.WakeRecord
		ld   string
		pt   int64
		want bool
	}{
		{"nil-record", nil, "950-0", 0, false},
		{"no-baseline", &bus.WakeRecord{}, "950-0", 2, false},
		{"same", mark(1, "", 1), "900-0", 2, false},
		{"seq-advanced", mark(1, "", 1), "900-1", 2, true},
		{"ms-advanced-numeric-not-lexical", &bus.WakeRecord{LastDelivered: "999-0", PendingTotal: 2}, "1000-0", 2, true},
		{"went-backwards", mark(1, "", 1), "899-9", 2, false},
		{"pel-decreased", mark(1, "", 1), "900-0", 1, true},
		{"pel-grew", mark(1, "", 1), "900-0", 9, false},
	}
	for _, c := range cases {
		o.LastDelivered, o.PendingTotal = c.ld, c.pt
		if got := Progressed(c.w, o); got != c.want {
			t.Errorf("%s: Progressed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIdentityChanged(t *testing.T) {
	cases := []struct {
		name string
		w    *bus.WakeRecord
		s    *bus.AgentSnapshot
		want bool
	}{
		{"nil-record", nil, snap(), false},
		{"record-without-pid", &bus.WakeRecord{}, snap(), false},
		{"nil-snapshot", mark(1, "", 1), nil, false},
		{"same", mark(1, "", 1), snap(), false},
		{"new-pid", mark(1, "", 1), &bus.AgentSnapshot{PID: 101, PIDStart: 5}, true},
		{"same-pid-new-start", mark(1, "", 1), &bus.AgentSnapshot{PID: 100, PIDStart: 6}, true},
		{"snapshot-lost-pid", mark(1, "", 1), &bus.AgentSnapshot{}, true},
	}
	for _, c := range cases {
		if got := IdentityChanged(c.w, c.s); got != c.want {
			t.Errorf("%s: IdentityChanged = %v, want %v", c.name, got, c.want)
		}
	}
}
