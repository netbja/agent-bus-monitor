package health

import (
	"strings"
	"testing"
)

// cleaning-up-agents §10 (spec docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md),
// step 1c: agents-lost lists every lost agent with its reason, whatever
// lost it (a holder event or not); parked and stopped are never a fault; a
// read that fails is unknown.
func TestEvaluateAgentsLost(t *testing.T) {
	in := AgentsLostInput{Agents: []PresenceAgent{
		{Name: "docs", Presence: "lost", Reason: "owns skill (accepted)"},
		{Name: "architect", Presence: "stopped"},
		{Name: "coder", Presence: "running"},
		{Name: "sec", Presence: "parked"},
		{Name: "coder2", Presence: "lost", Reason: "required: self-repair relaunches it"},
	}}
	o := EvaluateAgentsLost(in)
	if o.Status != Warn || o.Detail != "2 lost: coder2 (required: self-repair relaunches it); docs (owns skill (accepted))" {
		t.Fatalf("%+v", o)
	}
	if d, ok := ByID("agents-lost"); !ok || d.Handler != "dispatch" || d.Place(Warn) != PlaceInHand || d.Interval.Minutes() != 1 {
		t.Fatalf("the definition: %+v", d)
	}
	none := EvaluateAgentsLost(AgentsLostInput{Agents: []PresenceAgent{{Name: "a", Presence: "stopped"}, {Name: "b", Presence: "parked"}}})
	if none.Status != OK || strings.Contains(none.Detail, "lost") {
		t.Fatalf("none lost: %+v", none)
	}
	if u := EvaluateAgentsLost(AgentsLostInput{ReadErr: "agents unavailable"}); u.Status != Unknown || u.Detail != "agents unavailable" {
		t.Fatalf("a failed read: %+v", u)
	}
}
