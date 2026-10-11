package mission

import (
	"slices"
	"testing"
)

type row struct {
	from State
	ev   Event
	left int
	to   State
	acts []Action
}

// expected is the plan's transition table, written out independently of the
// implementation: every pair not listed here must be refused.
func expected() []row {
	var rs []row
	add := func(from State, ev Event, left int, to State, acts ...Action) {
		rs = append(rs, row{from, ev, left, to, acts})
	}
	add(Pending, EvDepsIntegrated, 1, Ready)
	add(Ready, EvRequestSent, 1, Requested)
	add(Requested, EvAccepted, 1, Accepted)
	add(Requested, EvAgentBlocked, 1, Blocked, ActEscalate)
	add(Accepted, EvAgentBlocked, 1, Blocked, ActEscalate)
	add(Accepted, EvDelivered, 1, Delivered)
	add(Delivered, EvCheckStarted, 1, Checking)
	add(Checking, EvCheckPassed, 1, InReview, ActOpenReview)
	add(Checking, EvCheckFailed, 1, Requested, ActSupersede, ActSendRequest)
	add(Checking, EvCheckFailed, 0, Blocked, ActEscalate)
	add(InReview, EvApproved, 1, Approved, ActResolveReview)
	add(InReview, EvReviewRejected, 1, Requested, ActResolveReview, ActSupersede, ActSendRequest)
	add(InReview, EvReviewRejected, 0, Blocked, ActResolveReview, ActEscalate)
	add(Approved, EvIntegrateStarted, 1, Integrating)
	add(Integrating, EvIntegrated, 1, Integrated)
	add(Integrating, EvIntegrateFailed, 1, Blocked, ActEscalate)
	add(Blocked, EvRetry, 1, Requested, ActSupersede, ActSendRequest)
	for _, s := range []State{Pending, Ready, Requested, Accepted, Delivered, Checking, Approved, Integrating, Blocked} {
		if s == Pending {
			add(s, EvAbort, 1, Aborted)
		} else {
			add(s, EvAbort, 1, Aborted, ActSupersede)
		}
	}
	add(InReview, EvAbort, 1, Aborted, ActSupersede, ActResolveReview)
	add(Pending, EvSkip, 1, Integrated)
	add(Ready, EvSkip, 1, Integrated)
	for _, s := range []State{Requested, Accepted, Delivered, Checking, Blocked} {
		add(s, EvSkip, 1, Integrated, ActSupersede)
	}
	add(InReview, EvSkip, 1, Integrated, ActSupersede, ActResolveReview)
	add(Accepted, EvReassign, 1, Requested, ActSupersede, ActSendRequest)
	add(Blocked, EvReassign, 1, Requested, ActSupersede, ActSendRequest)
	add(Accepted, EvTimedOut, 1, Blocked, ActEscalate)
	return rs
}

func TestNextTable(t *testing.T) {
	listed := map[[2]string]bool{}
	for _, r := range expected() {
		listed[[2]string{string(r.from), string(r.ev)}] = true
		got, acts, err := Next(r.from, r.ev, Ctx{AttemptsLeft: r.left})
		if err != nil || got != r.to || !slices.Equal(acts, r.acts) {
			t.Errorf("Next(%s, %s, left=%d) = %s %v %v; want %s %v", r.from, r.ev, r.left, got, acts, err, r.to, r.acts)
		}
	}
	for _, s := range AllStates {
		for _, ev := range AllEvents {
			if listed[[2]string{string(s), string(ev)}] {
				continue
			}
			got, acts, err := Next(s, ev, Ctx{AttemptsLeft: 1})
			if err == nil || got != s || acts != nil {
				t.Errorf("Next(%s, %s) accepted: %s %v %v", s, ev, got, acts, err)
			}
		}
	}
}

func TestAttemptsExhausted(t *testing.T) {
	if s, acts, _ := Next(Checking, EvCheckFailed, Ctx{AttemptsLeft: 0}); s != Blocked || !slices.Equal(acts, []Action{ActEscalate}) {
		t.Fatalf("left 0: %s %v", s, acts)
	}
	if s, _, _ := Next(Checking, EvCheckFailed, Ctx{AttemptsLeft: 1}); s != Requested {
		t.Fatalf("left 1: %s", s)
	}
}
