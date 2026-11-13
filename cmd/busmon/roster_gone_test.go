package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// retire-forget-roster (the person's bug): retire --forget deletes the
// agent's {p}:agents row, but busmon kept the row it had and showed the
// retired pop "idle 4h" until a day's silence made it dormant. An agent that
// was registered and has left the hash leaves the roster at the next read;
// an armed one (a subscriber still listens), one that never was registered
// (a service that only reports) and every row on a failed read are kept.
func TestRosterDropsAnAgentGoneFromTheHash(t *testing.T) {
	now := time.Now().UnixMilli()
	both := map[string]bus.AgentSnapshot{"coder": {State: "working", TS: now, PID: 42}, "security": {State: "idle", TS: now - 4*3600*1000, PID: 43}}
	f := &fakeReader{calls: map[string]int{}, snaps: both}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	st.agents["conductor"] = &agentState{state: "working"} // reports only: never in the hash
	pollOnce(context.Background(), f, st, 1)
	if st.agents["security"] == nil || !st.agents["security"].registered {
		t.Fatalf("security before the retire: %+v", st.agents["security"])
	}
	// A failed read of the hash: nothing is dropped.
	f.agentsErr = errors.New("timeout")
	f.snaps = map[string]bus.AgentSnapshot{"coder": both["coder"]}
	pollOnce(context.Background(), f, st, 2)
	if st.agents["security"] == nil {
		t.Fatal("a failed read dropped security")
	}
	// retire --forget: the row is gone from the hash.
	f.agentsErr = nil
	pollOnce(context.Background(), f, st, 3)
	if st.agents["security"] != nil {
		t.Fatalf("security still in the roster after leaving the hash: %+v", st.agents["security"])
	}
	if st.agents["coder"] == nil || st.agents["conductor"] == nil {
		t.Fatalf("other rows dropped: %v", st.agents)
	}
	// An agent gone from the hash but armed still listens: kept.
	f.snaps = both
	pollOnce(context.Background(), f, st, 4)
	f.snaps = map[string]bus.AgentSnapshot{"coder": both["coder"]}
	f.armed = map[string]string{"security": "1"}
	pollOnce(context.Background(), f, st, 5)
	if st.agents["security"] == nil {
		t.Fatal("an armed agent was dropped")
	}
}
