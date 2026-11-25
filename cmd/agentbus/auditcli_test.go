package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// audit step 2: a CLI act's line from its exit code: 0 ok, 2 a usage
// error (no act: no line), any other failed, its code named.
func TestAuditFromExit(t *testing.T) {
	e := bus.AuditEntry{Action: "retire", Actor: "master", Target: "security"}
	for _, c := range []struct {
		code    int
		outcome string
		detail  string
		write   bool
	}{
		{0, bus.AuditOK, "the pop ended", true},
		{2, "", "", false},
		{1, bus.AuditFailed, "the pop ended, exit 1", true},
		{3, bus.AuditFailed, "the pop ended, exit 3", true},
	} {
		got, ok := auditFromExit(e, "the pop ended", c.code)
		if ok != c.write || ok && (got.Outcome != c.outcome || got.Detail != c.detail || got.Action != "retire" || got.Target != "security") {
			t.Errorf("exit %d: %+v %v", c.code, got, ok)
		}
	}
}

// forceAct is the audit line of a --force verb, or none: agents forget,
// swap, mission load (master's list); any other verb, or no --force, none.
func TestForceAct(t *testing.T) {
	for _, c := range []struct {
		cmd    string
		rest   []string
		target string
		detail string
	}{
		{"agents", []string{"forget", "coder", "--force"}, "coder", "agents forget --force"},
		{"swap", []string{"coder", "--profile", "x", "--force"}, "coder", "swap --force"},
		{"mission", []string{"load", "plan.md@abc", "--force", "my free reason"}, "plan.md@abc", "mission load --force"},
		{"agents", []string{"forget", "coder"}, "", ""},
		{"agents", []string{"coder", "--force"}, "", ""},
		{"term", []string{"record", "claude", "f", "--force"}, "", ""},
		{"mission", []string{"retry", "c/1", "--force"}, "", ""},
	} {
		e, ok := forceAct(c.cmd, c.rest)
		if ok != (c.target != "") || ok && (e.Action != "force" || e.Target != c.target || e.Detail != c.detail) {
			t.Errorf("%s %v: %+v %v", c.cmd, c.rest, e, ok)
		}
	}
}
