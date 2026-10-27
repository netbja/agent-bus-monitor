package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A8(b): an agent deferred for "identity mismatch" while messages wait for it
// earns the person one desktop notification per episode (the registered
// pid:pid_start): its escalation goes to master, useless when master is the
// one lost. Nothing is written to a stream; the mark ends with the mismatch.
func TestIdentityMismatchNotifiesOncePerEpisode(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "ident-holder", desk, 1000, 1000)
	snap := &bus.AgentSnapshot{State: "idle", PID: 42, PIDStart: 7, Pane: "w1:p1"}
	mismatch := Decision{Action: ActDefer, Rule: 6, Reason: "identity mismatch"}
	s := &step{agent: "master", snap: snap, o: bus.Obligation{Unread: 2}}
	if err := l.identityNotice(ctx, s, mismatch); err != nil {
		t.Fatal(err)
	}
	if got := desk.Sent(); len(got) != 1 || !strings.Contains(got[0], "master") || !strings.Contains(got[0], "agentbus up "+b.Project()+" --resume") {
		t.Fatalf("desktop: %q", got)
	}
	// The same episode, read on the next pass: silent.
	s.w.Reason = "identity mismatch"
	if err := l.identityNotice(ctx, s, mismatch); err != nil {
		t.Fatal(err)
	}
	if got := desk.Sent(); len(got) != 1 {
		t.Fatalf("same episode rang again: %q", got)
	}
	// Nothing waiting: no notification for a mismatch alone.
	quiet := &step{agent: "coder", snap: &bus.AgentSnapshot{PID: 9, PIDStart: 1, Pane: "w1:p2"}}
	if err := l.identityNotice(ctx, quiet, mismatch); err != nil || len(desk.Sent()) != 1 {
		t.Fatalf("no pending messages rang: %v %q", err, desk.Sent())
	}
	// The mismatch ends (another decision): the mark goes, so a later one rings.
	if err := l.identityNotice(ctx, s, Decision{Action: ActDefer, Rule: 8, Reason: "agent working"}); err != nil {
		t.Fatal(err)
	}
	if marks, _ := b.QuotaMarks(ctx); marks[identMark("master")] != "" {
		t.Fatalf("mark kept after the mismatch ended: %v", marks)
	}
	s.w.Reason = ""
	if err := l.identityNotice(ctx, s, mismatch); err != nil || len(desk.Sent()) != 2 {
		t.Fatalf("a new episode did not ring: %v %q", err, desk.Sent())
	}
	for _, k := range []string{"cmd", "notify"} {
		if n := len(entries(t, r, b, k)); n != 0 {
			t.Fatalf("%d %s entries written: desktop only", n, k)
		}
	}
	// A dispatcher that lost the lease rings nothing.
	l2 := &loop{b: b, token: "not-the-holder", last: map[string]string{}, cfg: l.cfg}
	other := &step{agent: "sentinel", snap: &bus.AgentSnapshot{PID: 5, PIDStart: 2, Pane: "w1:p3"}, o: bus.Obligation{Unread: 1}}
	if err := l2.identityNotice(ctx, other, mismatch); !errors.Is(err, bus.ErrNotHolder) || len(desk.Sent()) != 2 {
		t.Fatalf("a non-holder: %v %q", err, desk.Sent())
	}
}
