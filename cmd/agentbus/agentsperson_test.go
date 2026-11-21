package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// person-never-agent: `agentbus agents` lists no entry under the person's
// recorded name, whatever terminal it names; asked for by name, it is shown
// (the operator inspects it before agents forget).
func TestAgentsListLeavesThePersonOut(t *testing.T) {
	all := map[string]bus.AgentSnapshot{
		"master":  {State: "idle", Pane: "t2"},
		"charlie": {State: "waiting", Pane: "old-holder/t4"},
	}
	m, missing := listedAgents(all, nil, "charlie")
	if _, ok := m["charlie"]; ok || len(m) != 1 || len(missing) != 0 {
		t.Fatalf("all: %v missing %v", m, missing)
	}
	if _, ok := all["charlie"]; !ok {
		t.Fatal("the caller's map was changed")
	}
	if m, missing := listedAgents(all, []string{"charlie"}, "charlie"); len(m) != 1 || len(missing) != 0 {
		t.Fatalf("by name: %v missing %v", m, missing)
	}
	if m, _ := listedAgents(all, nil, ""); len(m) != 2 {
		t.Fatalf("no person: %v", m)
	}
}
