package main

import (
	"context"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// human-not-an-agent: an entry under the person's name written from another
// agent's terminal (agentbus ask <person> in master's) is not listed as an
// agent; an agent of the person's name with its own terminal still is.
func TestPollDropsThePersonsBorrowedEntry(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"master":  {State: "idle", TS: 100, Pane: "t4"},
		"charlie": {State: "waiting", Message: "1 question(s): x", TS: 101, Pane: "t4"},
	}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}, person: "charlie"}
	pollOnce(context.Background(), f, st, 1)
	if _, ok := st.agents["charlie"]; ok {
		t.Fatalf("the person listed as an agent: %+v", st.agents["charlie"])
	}
	if _, ok := st.agents["master"]; !ok {
		t.Fatal("master lost")
	}
	// Its own terminal: an agent that has the person's name stays.
	f.snaps["charlie"] = bus.AgentSnapshot{State: "working", TS: 102, Pane: "t7"}
	pollOnce(context.Background(), f, st, 2)
	if _, ok := st.agents["charlie"]; !ok {
		t.Fatal("an agent with its own terminal dropped")
	}
}
