package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// audit step 2b: a host-level act (update, setup's reinstall) writes its
// line on the bus of each project it touched, once; an invalid project name
// is skipped without stopping the others.
func TestAuditHostAct(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	ps := []string{"ha" + stamp, "hb" + stamp}
	for _, p := range ps {
		t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), c, p) })
	}
	e := bus.AuditEntry{Action: "update", Actor: "charlie", Target: "v0.2.0", Outcome: bus.AuditOK, Detail: "v0.1.0 -> v0.2.0, installed, snapshot x"}
	auditHostAct(context.Background(), c, e, []string{ps[0], "Not A Name", ps[1]})
	for _, p := range ps {
		b, err := bus.Open(c, p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.AuditEntries(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Action != "update" || got[0].Actor != "charlie" || got[0].Target != "v0.2.0" ||
			got[0].Outcome != bus.AuditOK || got[0].Detail != e.Detail {
			t.Fatalf("%s: %+v", p, got)
		}
	}
}

// setup's install writes a reinstall line only over an existing install
// (agentbus already in the bin dir), never in a dry run: ok on success,
// failed with its exit otherwise.
func TestReinstallAct(t *testing.T) {
	for _, c := range []struct {
		had, dry bool
		code     int
		outcome  string
		detail   string
	}{
		{false, false, 0, "", ""},
		{true, true, 0, "", ""},
		{true, false, 0, bus.AuditOK, "setup install over an existing install"},
		{true, false, 1, bus.AuditFailed, "setup install over an existing install, exit 1"},
	} {
		e, ok := reinstallAct(c.had, c.dry, c.code, "v0.34.0-3-gabc", "charlie")
		if ok != (c.outcome != "") || ok && (e.Action != "reinstall" || e.Actor != "charlie" || e.Target != "v0.34.0-3-gabc" ||
			e.Outcome != c.outcome || e.Detail != c.detail) {
			t.Errorf("%+v: %+v %v", c, e, ok)
		}
	}
}
