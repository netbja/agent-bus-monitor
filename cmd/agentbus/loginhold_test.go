package main

import (
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// The stall checks learn the login hold (login-updates r9 §3.6): with only
// hold-login:<agent> set, each reader excuses that agent as it does a quota
// hold, and says why.

func (w *stallWorld) loginHold(agent string) {
	w.t.Helper()
	if err := w.b.FencedQuotaSet(context.Background(), "stall-holder", bus.LoginHoldField(agent), "1794777000000"); err != nil {
		w.t.Fatal(err)
	}
}

// team-stall: the ready task's owner is held, so the team is not stalled on it.
func TestTeamStallLoginHeldOwner(t *testing.T) {
	w := newStallWorld(t)
	w.env["AGENT_BUS_QUIET_AFTER"] = "0"
	w.loginHold("coder")
	if out := w.run(w.s.teamStall); out.Status == health.Fail || !strings.Contains(out.Detail, "t1 (coder, login held)") {
		t.Fatalf("login-held owner: %+v", out)
	}
}

// quiet-agent: a drained directive and silence is no alarm for a held agent.
func TestQuietAgentLoginHeld(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "do the next task"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	w.loginHold("coder")
	if out := w.run(w.s.quietAgent); out.Status != health.OK {
		t.Fatalf("login-held and silent: %+v", out)
	}
}

// idle-implementer: held, not passed over, and master is not woken.
func TestIdleImplementerLoginHeld(t *testing.T) {
	w := newStallWorld(t)
	w.s.implementers = func() ([]string, error) { return []string{"coder"}, nil }
	ctx := context.Background()
	if _, err := w.b.Request(ctx, "agy-client-impl", "master", "coder", "", "implement agy", 0); err != nil {
		t.Fatal(err)
	}
	w.loginHold("coder")
	if out := w.run(w.s.idleImplementer); out.Status != health.OK || out.Detail != "coder held: its login expired" {
		t.Fatalf("held: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "idle-implementer"); err != nil {
		t.Fatal(err)
	}
	if got := w.DispatchDirectives("master"); len(got) != 0 {
		t.Fatalf("master woken for a login-held implementer: %q", got)
	}
}

// CannotAct: a login-held master cannot act on a stall; the person is told why.
func TestStallNoticeWhenMasterLoginHeld(t *testing.T) {
	w := newStallWorld(t)
	rec := w.PutStall()
	w.loginHold("master")
	if err := w.s.stallNotify(context.Background(), "stall-holder", rec); err != nil {
		t.Fatal(err)
	}
	if got := w.PersonNotices(); len(got) != 1 || !strings.Contains(got[0], "master cannot act (master is held: its Claude login expired)") {
		t.Fatalf("notices %q", got)
	}
}

// idle-master: a login-held master with work in hand is not woken.
func TestIdleMasterLoginHeld(t *testing.T) {
	w := idleMasterWorld(t)
	ctx := context.Background()
	head := "fa2d796f0123456789abcdef0123456789abcdef"
	if err := w.b.FencedPutMergeQueue(ctx, "stall-holder", bus.MergeQueueRecord{Source: "git", Forge: "none", Base: "main",
		Undelivered: []bus.MergeUndelivered{{Subject: "herdr-6c2-2", Head: head}}}); err != nil {
		t.Fatal(err)
	}
	w.loginHold("master")
	out := w.run(w.s.idleMaster)
	if err := w.s.stallWake(ctx, "stall-holder", "idle-master"); err != nil {
		t.Fatal(err)
	}
	if got := w.DispatchDirectives("master"); len(got) != 0 || out.Status != health.OK || out.Detail != "master held: its login expired" {
		t.Fatalf("login-held master: %+v, woken %q", out, got)
	}
}
