package main

import (
	"errors"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// audit step 2 (spec §2): agentbus deliver writes push after the push and
// deliver once the request is open, each one line; a delivery refused for
// its approvals writes deliver, outcome refused; a push the remote refuses
// writes push, outcome failed.
func TestDeliverAudits(t *testing.T) {
	audited := func(w *deliverWorld) *[]bus.AuditEntry {
		var got []bus.AuditEntry
		w.deps.Audit = func(e bus.AuditEntry) { got = append(got, e) }
		return &got
	}
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	got := audited(w)
	if code, _, errw := w.run("master", "coder/x", "--task", "t1"); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if len(*got) != 2 {
		t.Fatalf("lines %+v, want push then deliver", *got)
	}
	p, d := (*got)[0], (*got)[1]
	if p.Action != "push" || p.Actor != "master" || p.Target != "coder/x" || p.Outcome != bus.AuditOK || p.Detail != "head 11111111 to origin" {
		t.Fatalf("push line %+v", p)
	}
	if d.Action != "deliver" || d.Actor != "master" || d.Target != "pr 12" || d.Outcome != bus.AuditOK || d.Detail != "coder/x head 11111111 into main" {
		t.Fatalf("deliver line %+v", d)
	}

	w = newDeliverWorld(t, "git@github.com:o/r.git")
	w.verdicts = w.verdicts[:1] // one approval: refused
	got = audited(w)
	if code, _, _ := w.run("master", "coder/x"); code != 3 {
		t.Fatalf("one approval delivered: %d", code)
	}
	if len(*got) != 1 || (*got)[0].Action != "deliver" || (*got)[0].Outcome != bus.AuditRefused || (*got)[0].Detail != "coder/x head 11111111: not approved" {
		t.Fatalf("refused line %+v", *got)
	}

	w = newDeliverWorld(t, "git@github.com:o/r.git")
	w.r.cmds["git push origin "+dlvH+":refs/heads/coder/x"] = dlvOut{out: "rejected", err: errors.New("exit status 1")}
	got = audited(w)
	if code, _, _ := w.run("master", "coder/x"); code != 1 {
		t.Fatal("a refused push delivered")
	}
	if len(*got) != 1 || (*got)[0].Action != "push" || (*got)[0].Outcome != bus.AuditFailed {
		t.Fatalf("failed push line %+v", *got)
	}
}
