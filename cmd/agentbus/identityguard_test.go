package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// A8(a): inside a herdr pane with no AGENT_BUS_AGENT, a command that writes
// as the sender would post as the default "anonymous" (the agent's own name
// lost); it is refused with the fix, while read-only commands still run.
func TestIdentityGuard(t *testing.T) {
	pane := map[string]string{"AGENTBUS_TERM_ID": "w2:pA"}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	writes := [][]string{
		{"status", "idle", "x"}, {"report", "coder", "x"}, {"notify", "x"}, {"cmd", "coder", "x"},
		{"reply", "--ref", "1", "master", "x"}, {"challenge", "foureyes", "x"}, {"verdict", "--subject", "s", "coder", "approve"},
		{"board", "claim", "t"}, {"request", "accept", "t"}, {"gate", "close", "--mine"}, {"pilot", "claim"},
		{"plan", "done", "t"}, {"pause", "--for", "1h"}, {"shutdown"}, {"ask", "x"}, {"health", "note", "x"}, {"health", "accept", "disk", "x"},
		{"something-new"},
	}
	for _, w := range writes {
		err := identityGuard(env(pane), w[0], w[1:])
		if err == nil {
			t.Errorf("%v allowed as anonymous in a pane", w)
			continue
		}
		for _, want := range []string{"anonymous", "AGENT_BUS_AGENT", "agentbus up", "--resume"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: message %q lacks %q", w, err, want)
			}
		}
	}
	reads := [][]string{
		{"reports"}, {"thread", "1"}, {"verdicts"}, {"agents"}, {"board"}, {"board", "--json"}, {"request"},
		{"gate", "coder"}, {"pilot", "status"}, {"plan", "show"}, {"plan"}, {"health"}, {"health", "list"}, {"health", "detail", "services"}, {"health", "log"}, {"health", "warns"}, {"health", "accept", "disk", "--as", "charlie", "x"},
		{"asks"}, {"swaps"}, {"usage"}, {"budget"}, {"listen"}, {"subscribe", "--drain", "coder"}, {"pane", "coder"},
		{"roles", "doctor", "--publish"}, {"roles", "adopt", "x", "--ask", "1"}, // no sender in what they write
	}
	for _, r := range reads {
		if err := identityGuard(env(pane), r[0], r[1:]); err != nil {
			t.Errorf("read-only %v refused: %v", r, err)
		}
	}
	// Outside a pane with the person's identity, or with an agent's, nothing is
	// refused (outside a pane with no identity at all: P2-11, identitysender_test).
	for _, m := range []map[string]string{{config.EnvName("identity.as"): "charlie", "XDG_CONFIG_HOME": t.TempDir()}, {"AGENTBUS_TERM_ID": "w2:pA", "AGENT_BUS_AGENT": "coder"}} {
		if err := identityGuard(env(m), "status", []string{"idle", "x"}); err != nil {
			t.Errorf("env %v: %v", m, err)
		}
	}
	// AGENT_BUS_AGENT=anonymous is the same nobody as unset (P2-11, foureyes):
	// refused in a terminal as unset is.
	if err := identityGuard(env(map[string]string{"AGENTBUS_TERM_ID": "w2:pA", "AGENT_BUS_AGENT": "anonymous"}), "notify", []string{"x"}); err == nil {
		t.Error("an explicit anonymous allowed in a terminal")
	}
}

// merge-queue reads, but drop and undrop write as their caller (master-idle):
// the identity guard checks them.
func TestMergeQueueDropIsNotReadOnly(t *testing.T) {
	if !readOnlyCommand("merge-queue", []string{"--json"}) || readOnlyCommand("merge-queue", []string{"drop", "9b7106db", "x"}) || readOnlyCommand("merge-queue", []string{"undrop", "9b7106db"}) {
		t.Fatal("merge-queue drop/undrop read as read-only, or the listing not")
	}
}
