package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// From inside an agent's terminal, each host command becomes one host
// directive for its project, with only the words the closed set carries.
func TestHostOpFromArgs(t *testing.T) {
	cases := []struct {
		args    []string
		project string
		want    bus.HostOp
	}{
		{[]string{"up", "demo"}, "demo", bus.HostOp{Op: bus.HostUp, From: "coder"}},
		{[]string{"pop", "architect"}, "env", bus.HostOp{Op: bus.HostPop, Agent: "architect", From: "coder"}},
		{[]string{"pop", "architect", "--profile", "opus"}, "env", bus.HostOp{Op: bus.HostPop, Agent: "architect", Profile: "opus", From: "coder"}},
		{[]string{"reload", "reviewer"}, "env", bus.HostOp{Op: bus.HostReload, Agent: "reviewer", From: "coder"}},
		{[]string{"swap", "coder", "--profile", "kimi"}, "env", bus.HostOp{Op: bus.HostSwap, Agent: "coder", Profile: "kimi", From: "coder"}},
		{[]string{"swap", "coder", "--back"}, "env", bus.HostOp{Op: bus.HostSwap, Agent: "coder", From: "coder"}},
		{[]string{"retire", "architect"}, "env", bus.HostOp{Op: bus.HostRetire, Agent: "architect", From: "coder"}},
		{[]string{"suspend"}, "env", bus.HostOp{Op: bus.HostSuspend, From: "coder"}},
		{[]string{"suspend", "--project", "other"}, "other", bus.HostOp{Op: bus.HostSuspend, From: "coder"}},
		{[]string{"resume"}, "env", bus.HostOp{Op: bus.HostResume, From: "coder"}},
		{[]string{"holder", "drain", "demo"}, "demo", bus.HostOp{Op: bus.HostDrain, From: "coder"}},
		{[]string{"holder", "drain", "demo", "--ignore-background"}, "demo", bus.HostOp{Op: bus.HostDrain, IgnoreBackground: true, From: "coder"}},
	}
	for _, c := range cases {
		o, project, ok, err := hostOpFromArgs(c.args, "env", "coder")
		if err != nil || !ok || o != c.want || project != c.project {
			t.Errorf("%q: got %+v %q %v %v, want %+v %q", c.args, o, project, ok, err, c.want, c.project)
		}
	}
}

// Commands the relay does not carry run as they always did; a host command
// with words the closed set cannot carry is refused, never shortened.
func TestHostOpFromArgsElse(t *testing.T) {
	for _, args := range [][]string{{"report", "coder", "x"}, {"resume", "back", "to", "work"}, {"resume", "--all"}, {"holder"}, {"swap", "--help"}} {
		if _, _, ok, err := hostOpFromArgs(args, "env", "coder"); ok || err != nil {
			t.Errorf("%q: relayed (%v, %v), want run as it is", args, ok, err)
		}
	}
	for _, args := range [][]string{{"reload", "coder", "--fresh"}, {"retire", "architect", "--now"}, {"swap", "coder"}, {"up", "demo", "--resume"}, {"pop"}} {
		_, _, ok, err := hostOpFromArgs(args, "env", "coder")
		if !ok || err == nil || !strings.Contains(err.Error(), "inside an agent's terminal") {
			t.Errorf("%q: got ok=%v err=%v, want a refusal", args, ok, err)
		}
	}
}

// Outside a terminal the holder started, nothing is relayed; inside, a
// host command the closed set cannot carry exits 2 before any broker.
func TestRelayFromTerminal(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var out, errw strings.Builder
	if _, ok := relayFromTerminal(t.Context(), []string{"reload", "coder"}, "", "demo", env(nil), &out, &errw); ok {
		t.Fatal("relayed outside a terminal")
	}
	in := env(map[string]string{"AGENTBUS_TERM_ID": "t1", "AGENT_BUS_AGENT": "sentinel"})
	if _, ok := relayFromTerminal(t.Context(), []string{"report", "coder", "x"}, "", "demo", in, &out, &errw); ok {
		t.Fatal("relayed a command that is not a host command")
	}
	code, ok := relayFromTerminal(t.Context(), []string{"reload", "coder", "--fresh"}, "", "demo", in, &out, &errw)
	if !ok || code != 2 || !strings.Contains(errw.String(), "inside an agent's terminal") {
		t.Fatalf("reload --fresh in a terminal: %d %v %q", code, ok, errw.String())
	}
}
