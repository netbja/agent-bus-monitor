package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// Item 22, cleaning-up-agents §3, §10: agentbus agents shows an agent whose
// holder terminal is gone by its presence and reason, never its last 'idle'
// and never 'no terminal'; with the holder not read (nil), the declared
// states as before.
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
	owes := map[string]bus.Owes{"architect": {Tasks: []string{"spec (accepted)"}}, "coder": {}}
	out := agentsTable(m, time.Now(), presences(m, live, owes, nil, true))
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "architect") && (!strings.Contains(l, " lost ") || !strings.Contains(l, "owns spec (accepted)")):
			t.Errorf("architect: %q", l)
		case strings.HasPrefix(l, "coder") && !strings.Contains(l, "idle"):
			t.Errorf("coder: %q", l)
		}
	}
	if strings.Contains(out, "no terminal") {
		t.Fatalf("'no terminal' shown: %q", out)
	}
	if out := agentsTable(m, time.Now(), presences(m, nil, owes, nil, true)); strings.Contains(out, "lost") || strings.Count(out, "idle") != 2 {
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

// The roles file unread: the role's required flag is unknown, so an agent
// without a terminal owing nothing is lost, never stopped (fail closed); with
// it, an optional one is stopped and a required one lost.
func TestAgentsPresenceRoles(t *testing.T) {
	m := map[string]bus.AgentSnapshot{"docs": {State: "idle", Pane: "t4"}, "coder": {State: "idle", Pane: "t2"}}
	live := map[string]bool{}
	owes := map[string]bus.Owes{"docs": {Unknown: []string{bus.UnknownLosable}}, "coder": {}} // as bus.ReadOwes reads them in this step
	if p := presences(m, live, owes, nil, true)["docs"]; p.State != bus.PresenceLost || p.Reason != "unknown: roles" {
		t.Fatalf("no roles file, behind an unknown item 5 (reviewer-codex): %+v", p)
	}
	if !slices.Equal(owes["docs"].Unknown, []string{bus.UnknownLosable}) {
		t.Fatalf("the caller's owes changed: %v", owes["docs"].Unknown)
	}
	read := map[string]bus.Owes{"docs": {}, "coder": {}} // every item read, as from step 2
	p := presences(m, live, read, driftManifest(t), false)
	if p["docs"].State != bus.PresenceStopped || p["coder"].State != bus.PresenceLost || p["coder"].Reason != "required: agentbus up brings it back" {
		t.Fatalf("with roles: %+v", p)
	}
	js := agentsJSON(m, p)
	if js["docs"].Presence != bus.PresenceStopped || js["coder"].PresenceReason == "" || js["coder"].State != "idle" {
		t.Fatalf("json: %+v", js)
	}
}

// silentHolder: no holder of the project answers.
type silentHolder struct{}

func (silentHolder) List(context.Context, string) ([]termhost.Term, error) { return nil, nil }
func (silentHolder) Describe(context.Context) (termhost.HostInfo, error) {
	return termhost.HostInfo{}, nil
}
