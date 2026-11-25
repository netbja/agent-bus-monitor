package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// audit step 2b: restore --force writes one force line on each project
// whose keys it was asked to replace: refused on the person's no, ok once
// replaced (the line written after the restore, so the replaced trail
// keeps it); none without --force.
func TestRestoreCommandForceAudits(t *testing.T) {
	w := newRestoreCmdWorld(t)
	p := w.B.Project()
	c, _ := w.D.Dial()
	b, err := bus.Open(c, p)
	if err != nil {
		t.Fatal(err)
	}
	last := func() (bus.AuditEntry, int) {
		t.Helper()
		got := forceLines(t, b)
		if len(got) == 0 {
			return bus.AuditEntry{}, 0
		}
		return got[len(got)-1], len(got)
	}
	if code, out := w.restore(w.archive, "--project", p); code != 0 {
		t.Fatalf("restore: %d\n%s", code, out)
	}
	if _, n := last(); n != 0 {
		t.Fatalf("a restore without --force wrote %d force line(s)", n)
	}
	w.answer = "n\n"
	if code, out := w.restore(w.archive, "--project", p, "--force", "--redis-only"); code != 1 {
		t.Fatalf("force, no: %d\n%s", code, out)
	}
	if e, n := last(); n != 1 || e.Outcome != bus.AuditRefused || e.Actor != "person" || e.Target != p ||
		!strings.HasPrefix(e.Detail, "restore --force, ") || !strings.HasSuffix(e.Detail, " key(s) present, not confirmed") {
		t.Fatalf("after no: %+v (%d)", e, n)
	}
	w.answer = "y\n"
	if code, out := w.restore(w.archive, "--project", p, "--force", "--redis-only"); code != 0 {
		t.Fatalf("force, yes: %d\n%s", code, out)
	}
	if e, _ := last(); e.Outcome != bus.AuditOK || !strings.HasSuffix(e.Detail, " key(s) replaced") {
		t.Fatalf("after yes: %+v", e)
	}
}
