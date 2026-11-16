package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// herdr exit §4.2 ("Identity"): inside a terminal the holder started
// (AGENTBUS_TERM_ID), status records that terminal as the pane, and the
// host's own child as the verified process when it is an ancestor of this
// one and its start time is the one read now; anything else leaves the
// process unverified, and status still succeeds.
func TestStatusRecordsIdentityFromTheHolder(t *testing.T) {
	real := identTerms
	defer func() { identTerms = real }()
	parent := os.Getppid()
	start, err := dispatch.ProcStart(parent)
	if err != nil {
		t.Skip("no process start times here")
	}
	t.Setenv("HERDR_ENV", "")
	t.Setenv("AGENTBUS_TERM_ID", "t9")
	t.Setenv("AGENT_BUS_PROJECT", "demo")
	t.Setenv("AGENT_BUS_AGENT", "dev")
	terms := []termhost.Term{{ID: "k1/t9", Project: "demo", Name: "dev", PID: parent, PIDStart: start, Alive: true}}
	var asked string
	identTerms = func(_ context.Context, project string) ([]termhost.Term, error) { asked = project; return terms, nil }
	who := statusIdent(context.Background())
	if asked != "demo" || who.PID != parent || who.PIDStart != start || who.Pane != "k1/t9" || who.Terminal != "k1/t9" {
		t.Fatalf("from the holder: %+v (asked %q)", who, asked)
	}
	for name, ts := range map[string][]termhost.Term{
		"another start time": {{ID: "k1/t9", Name: "dev", PID: parent, PIDStart: start + 1, Alive: true}},
		"not an ancestor":    {{ID: "k1/t9", Name: "dev", PID: 999999999, PIDStart: 1, Alive: true}},
		"another agent's":    {{ID: "k1/t9", Name: "coder", PID: parent, PIDStart: start, Alive: true}},
		"another terminal":   {{ID: "k1/t8", Name: "dev", PID: parent, PIDStart: start, Alive: true}},
	} {
		terms = ts
		if who := statusIdent(context.Background()); who.PID != 0 || who.PIDStart != 0 || who.Pane != "t9" {
			t.Errorf("%s: %+v", name, who)
		}
	}
	identTerms = func(context.Context, string) ([]termhost.Term, error) { return nil, errors.New("no holder") }
	if who := statusIdent(context.Background()); who.PID != 0 || who.Pane != "t9" {
		t.Fatalf("holder unreadable: %+v", who)
	}
	t.Setenv("AGENTBUS_TERM_ID", "")
	if who := statusIdent(context.Background()); who.PID != 0 || who.Pane != "" {
		t.Fatalf("outside a terminal: %+v", who)
	}
}

// The status publish carries the agent's CLAUDE_CONFIG_DIR (login-updates
// §3.2); the bus keeps it only with a proof (TestMergeSnapshotClaudeConfigDir).
func TestStatusIdentCarriesClaudeConfigDir(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/srv/claude-b")
	if who := statusIdent(context.Background()); who.ClaudeConfigDir != "/srv/claude-b" {
		t.Fatalf("%+v", who)
	}
}
