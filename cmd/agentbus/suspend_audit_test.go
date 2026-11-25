package main

import (
	"context"
	"errors"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// forceLines are the project's audit lines of action force.
func forceLines(t *testing.T, b *bus.Bus) []bus.AuditEntry {
	t.Helper()
	es, err := b.AuditEntries(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []bus.AuditEntry
	for _, e := range es {
		if e.Action == "force" {
			out = append(out, e)
		}
	}
	return out
}

// audit step 2b: suspend --force writes one force line per project, the
// person its actor, the busy agents counted; without --force, none.
func TestSuspendForceAudits(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	if code, out, errw := w.run("--wait", "50ms", "--force"); code != 0 {
		t.Fatalf("suspend --force = %d\n%s\n%s", code, out, errw)
	}
	got := forceLines(t, w.b)
	if len(got) != 1 || got[0].Actor != "charlie" || got[0].Target != w.b.Project() || got[0].Outcome != bus.AuditOK ||
		got[0].Detail != "suspend --force, 1 agent(s) still busy" {
		t.Fatalf("force lines %+v", got)
	}

	quiet := newSuspendWorld(t)
	quiet.agent("coder", "idle")
	if code, out, errw := quiet.run("--wait", "50ms"); code != 0 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	if got := forceLines(t, quiet.b); len(got) != 0 {
		t.Fatalf("a suspend without --force wrote %+v", got)
	}
}

// A --force suspend that fails to stop a service records failed; a dry run
// records nothing.
func TestSuspendForceAuditsAFailure(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "idle")
	if code, _, _ := w.run("--dry-run", "--force"); code != 0 {
		t.Fatalf("dry run = %d", code)
	}
	if got := forceLines(t, w.b); len(got) != 0 {
		t.Fatalf("a dry run wrote %+v", got)
	}
	w.mgr.StopErr = func(_, k string) error {
		if k == service.Missions {
			return errors.New("unit busy")
		}
		return nil
	}
	if code, _, _ := w.run("--wait", "50ms", "--force"); code == 0 {
		t.Fatal("a stop that failed returned 0")
	}
	got := forceLines(t, w.b)
	if len(got) != 1 || got[0].Outcome != bus.AuditFailed || got[0].Detail != "suspend --force, 0 agent(s) still busy, exit 1" {
		t.Fatalf("force lines %+v", got)
	}
}
