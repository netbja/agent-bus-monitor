package main

import (
	"context"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// An agent with a pending hand-off record shows the flag `handoff` (lifecycle
// spec, part A: display only); a void record shows nothing.
func TestHandoffFlagOnlyWhilePending(t *testing.T) {
	snap := bus.AgentSnapshot{State: "idle", TS: 2000, PID: 42, PIDStart: 7, IdentSession: "session-0001"}
	rec := bus.Handoff{Report: "5-0", Session: "session-0001", PID: 42, PIDStart: 7, At: 1000}
	for name, c := range map[string]struct {
		rec  bus.Handoff
		want bool
	}{
		"pending":         {rec, true},
		"another session": {bus.Handoff{Report: "5-0", Session: "session-0000", PID: 42, PIDStart: 7, At: 1000}, false},
	} {
		f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{"coder": snap}, handoffs: map[string]bus.Handoff{"coder": c.rec}}
		st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
		pollOnce(context.Background(), f, st, 1) // a dispatcher-facts tick
		got := false
		for _, fl := range agentFlags(st.agents["coder"], false) {
			got = got || fl.Word == "handoff"
		}
		if got != c.want {
			t.Errorf("%s: handoff flag %v, want %v", name, got, c.want)
		}
	}
}
