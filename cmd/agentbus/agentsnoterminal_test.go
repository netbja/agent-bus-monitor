package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// Item 22: agentbus agents shows 'no terminal' for an agent whose holder
// terminal is gone, never its last 'idle'; with the holder not read
// (nil), the declared states as before.
func TestAgentsTableNoTerminal(t *testing.T) {
	m := map[string]bus.AgentSnapshot{
		"architect": {State: "idle", Pane: "t4"},
		"coder":     {State: "idle", Pane: "t2"},
	}
	live := liveTerminals([]termhost.Term{
		{ID: "t2", Project: "demo", Name: "coder", Alive: true},
		{ID: "t4", Project: "demo", Name: "architect", Alive: false}, // its program ended
		{ID: "t5", Project: "other", Name: "architect", Alive: true}, // another project's
	}, "demo")
	out := agentsTable(m, time.Now(), live)
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "architect") && !strings.Contains(l, bus.StateNoTerminal):
			t.Errorf("architect: %q", l)
		case strings.HasPrefix(l, "coder") && (!strings.Contains(l, "idle") || strings.Contains(l, bus.StateNoTerminal)):
			t.Errorf("coder: %q", l)
		}
	}
	if out := agentsTable(m, time.Now(), nil); strings.Contains(out, bus.StateNoTerminal) {
		t.Fatalf("holder not read: %q", out)
	}
	// No holder answers on this host: nothing is judged.
	if got := liveFrom(context.Background(), silentHolder{}, "demo"); got != nil {
		t.Fatalf("no holder: %v", got)
	}
	h := fake.New()
	if _, err := h.Start(context.Background(), termhost.Spec{Project: "demo", Name: "coder"}); err != nil {
		t.Fatal(err)
	}
	if got := liveFrom(context.Background(), h, "demo"); !got["coder"] || got["architect"] {
		t.Fatalf("a holder that answers: %v", got)
	}
}

// silentHolder: no holder of the project answers.
type silentHolder struct{}

func (silentHolder) List(context.Context, string) ([]termhost.Term, error) { return nil, nil }
func (silentHolder) Describe(context.Context) (termhost.HostInfo, error) {
	return termhost.HostInfo{}, nil
}
