package main

import (
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// idle-implementer read from a live project (test Redis): coder idle 20 min,
// master idle too; a request to coder warns and wakes master once; accepted,
// it is no longer passed over.
func TestIdleImplementerReadsALiveProject(t *testing.T) {
	w := newStallWorld(t)
	w.s.implementers = func() ([]string, error) { return []string{"coder"}, nil }
	ctx := context.Background()
	if out := w.run(w.s.idleImplementer); out.Status != health.OK || out.Detail != "nothing requested for coder" {
		t.Fatalf("nothing requested: %+v", out)
	}
	if _, err := w.b.Request(ctx, "agy-client-impl", "master", "coder", "", "implement agy", 0); err != nil {
		t.Fatal(err)
	}
	out := w.run(w.s.idleImplementer)
	if out.Status != health.Warn || out.Detail != "coder idle 20m; requested: agy-client-impl" {
		t.Fatalf("requested: %+v", out)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "idle-implementer"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 || !strings.Contains(got[0], "coder idle 20m while requested for it: agy-client-impl") {
		t.Fatalf("master woken %q", got)
	}
	if err := w.b.AcceptRequest(ctx, "agy-client-impl", "coder"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleImplementer); out.Status != health.OK {
		t.Fatalf("accepted: %+v", out)
	}
	w.env["AGENT_BUS_IDLE_AFTER"] = "0"
	if out := w.run(w.s.idleImplementer); out.Status != health.Unknown || out.Detail != "off (idle_after = 0)" {
		t.Fatalf("off: %+v", out)
	}
}

// The person's answer 2: an implementer is a role with the implement skill;
// in the shipped manifest, coder and coder2.
func TestShippedImplementers(t *testing.T) {
	m, err := rolecfg.Load("../../roles.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rolesWithSkill(m, "implement"), ","); got != "coder,coder2" {
		t.Fatalf("implementers %q", got)
	}
}
