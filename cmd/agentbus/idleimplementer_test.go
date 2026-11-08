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

// An implementer its provider's limit holds is not passed over: the reader
// maps the quota hold of its provider to the agent (foureyes, after PR 362),
// and no wake goes to master; the hold lifted, the warn comes back.
func TestIdleImplementerQuotaHeld(t *testing.T) {
	w := newStallWorld(t)
	w.s.implementers = func() ([]string, error) { return []string{"coder"}, nil }
	ctx := context.Background()
	if _, err := w.b.Request(ctx, "agy-client-impl", "master", "coder", "", "implement agy", 0); err != nil {
		t.Fatal(err)
	}
	if err := w.b.FencedQuotaSet(ctx, "stall-holder", "hold:anthropic", "1"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleImplementer); out.Status != health.OK || out.Detail != "coder held by its quota" {
		t.Fatalf("held: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "idle-implementer"); err != nil {
		t.Fatal(err)
	}
	if got := w.dispatchDirectives("master"); len(got) != 0 {
		t.Fatalf("master woken for a held implementer: %q", got)
	}
	if err := w.b.FencedQuotaDel(ctx, "stall-holder", "hold:anthropic"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.idleImplementer); out.Status != health.Warn {
		t.Fatalf("hold lifted: %+v", out)
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
