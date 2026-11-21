package main

import (
	"context"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// human-not-an-agent, person-never-agent: an entry under the person's name is
// never listed as an agent, whatever terminal it names: one written from
// another agent's terminal (agentbus ask <person> in master's), a pre-#590
// record of an old holder's terminal no agent shares now, or none at all.
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
	// A terminal of its own that no agent shares (an old holder's t4, where an
	// agent once wrote status as the person): still the person, not listed.
	f.snaps["charlie"] = bus.AgentSnapshot{State: "waiting", TS: 102, Pane: "old-holder/t4"}
	pollOnce(context.Background(), f, st, 2)
	if _, ok := st.agents["charlie"]; ok {
		t.Fatal("the person's entry on an unshared terminal listed as an agent")
	}
	// No terminal (the person's own shell): not listed either; an agent with
	// no terminal (a service that reports) stays.
	f.snaps["charlie"] = bus.AgentSnapshot{State: "working", TS: 103}
	f.snaps["sentinel"] = bus.AgentSnapshot{State: "idle", TS: 103}
	pollOnce(context.Background(), f, st, 3)
	if _, ok := st.agents["charlie"]; ok {
		t.Fatal("the person's terminal-less entry listed as an agent")
	}
	if _, ok := st.agents["sentinel"]; !ok {
		t.Fatal("an agent with no terminal dropped")
	}
}
