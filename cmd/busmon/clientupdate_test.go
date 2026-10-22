package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestUpdateFlagAndDetail(t *testing.T) {
	now := time.Now()
	u := &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: "2.1.306", Hash: "h", Source: "layout"}
	a := &agentState{state: "idle", stateAt: now, obsAt: now, clientUpdate: u}
	var words []string
	for _, f := range agentFlags(a, false) {
		words = append(words, f.Word)
	}
	if !strings.Contains(strings.Join(words, ","), "update") {
		t.Fatalf("flags %v", words)
	}
	if _, word, _, _ := stateLook(a); word != "idle" {
		t.Fatalf("declared state relabelled %q", word)
	}
	_, body := agentDetail("coder", a, false, nil, nil, now)
	for _, want := range []string{"2.1.305 running", "2.1.306 installed", "reload when idle", "/exit", "agentbus up <project> --resume"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail lacks %q:\n%s", want, body)
		}
	}
	// A stale reading says nothing.
	a.obsAt = now.Add(-10 * time.Minute)
	for _, f := range agentFlags(a, false) {
		if f.Word == "update" {
			t.Fatal("a stale observation flags an update")
		}
	}
}

func TestHelpExplainsTheUpdateFlag(t *testing.T) {
	if !strings.Contains(helpText(), "a newer client is installed than this agent runs") {
		t.Fatal("the legend does not explain update")
	}
}

// Only a reading of the agent's own process counts.
func TestPollKeepsTheUpdateOfTheAgentsProcess(t *testing.T) {
	u := &bus.ClientUpdate{Client: "claude-code", Running: "1", Installed: "2", Hash: "h"}
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"coder":  {State: "idle", TS: 1, Pane: "p1", PID: 7},
		"master": {State: "idle", TS: 1, Pane: "p2", PID: 8}},
		observed: map[string]bus.Observation{
			"coder":  {At: time.Now().UnixMilli(), PID: 7, Update: u},
			"master": {At: time.Now().UnixMilli(), PID: 99, Update: u}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	for i := 0; i < 5; i++ { // the dispatcher facts are read every 5th tick
		pollOnce(context.Background(), f, st, i)
	}
	if st.agents["coder"].clientUpdate == nil || st.agents["master"].clientUpdate != nil {
		t.Fatalf("coder %+v master %+v", st.agents["coder"].clientUpdate, st.agents["master"].clientUpdate)
	}
}
