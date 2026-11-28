package dispatchsvc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// cleaning-up-agents step 1c: the dispatcher's presence of each agent, as
// agentbus agents and busmon judge it, feeds agents-lost.
func TestPresenceAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.toml")
	toml := "schema_version = 2\n[profiles.p]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n" +
		"[roles.coder]\nprofile = \"p\"\ntier = \"boot\"\nrequired = true\n[roles.docs]\nprofile = \"p\"\ntier = \"pop\"\n[roles.architect]\nprofile = \"p\"\ntier = \"pop\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	man, err := rolecfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	snaps := map[string]bus.AgentSnapshot{"coder": {Pane: "k/t1"}, "docs": {Pane: "k/t2"}, "architect": {Pane: "k/t3"}, "sec": {Pane: "k/t4"}, "master": {Pane: "k/t5"}}
	live := map[string]bool{"master": true}
	owes := map[string]bus.Owes{"coder": {}, "docs": {Tasks: []string{"skill (accepted)"}}, "architect": {}, "master": {Tasks: []string{"x (accepted)"}}} // sec: not read
	got := map[string]health.PresenceAgent{}
	for _, a := range presenceAgents(snaps, live, owes, man, true) {
		got[a.Name] = a
	}
	for name, want := range map[string][2]string{
		"coder":     {bus.PresenceLost, "required: self-repair relaunches it"},
		"docs":      {bus.PresenceLost, "owns skill (accepted)"},
		"architect": {bus.PresenceStopped, ""},
		"sec":       {bus.PresenceLost, "unknown: owes"},
		"master":    {bus.PresenceRunning, ""},
	} {
		if a := got[name]; a.Presence != want[0] || a.Reason != want[1] {
			t.Errorf("%s: %+v, want %v", name, a, want)
		}
	}
	// No roles file: required unknown, named first (fail closed), even with
	// item 5 unknown as bus.ReadOwes reads it.
	asRead := map[string]bus.Owes{"architect": {Unknown: []string{bus.UnknownLosable}}}
	for _, a := range presenceAgents(snaps, live, asRead, nil, true) {
		if a.Name == "architect" && (a.Presence != bus.PresenceLost || a.Reason != "unknown: roles") {
			t.Errorf("no roles file: %+v", a)
		}
	}
	// No holder read: nothing is judged.
	for _, a := range presenceAgents(snaps, nil, owes, man, true) {
		if a.Presence != bus.PresenceRunning {
			t.Errorf("holder not read: %+v", a)
		}
	}
	// The evaluator reads bus's word for lost.
	o := health.EvaluateAgentsLost(health.AgentsLostInput{Agents: presenceAgents(snaps, live, owes, man, false)})
	if o.Status != health.Warn || o.Detail != "3 lost: coder (required: agentbus up brings it back); docs (owns skill (accepted)); sec (unknown: owes)" {
		t.Fatalf("%+v", o)
	}
}
