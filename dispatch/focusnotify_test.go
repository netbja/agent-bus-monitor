package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// focus-notice: a wake deferred because the agent's pane has the focus, its
// oldest message waiting FocusGrace, earns the person one desktop notice per
// oldest message; leaving the pane lets the next pass wake it (rule 8 defers
// only while focused). Nothing is written to a stream; the mark ends with the
// focus defer.
func TestFocusNoticeOncePerOldestMessage(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	desk := &fakeDesktop{}
	l := promptLoop(t, b, "focus-holder", desk, 1000, 1000)
	l.cfg.Prompt.FocusGrace = 30 * time.Second
	focused := Decision{Action: ActDefer, Rule: 8, Reason: "pane focused"}
	snap := &bus.AgentSnapshot{State: "idle", PID: 42, PIDStart: 7, Pane: "w1:p1"}
	s := &step{agent: "coder", snap: snap, o: bus.Obligation{Unread: 2, OldestID: "100-0"}}
	s.x.OldestAge = 10 * time.Second
	if err := l.focusNotice(ctx, s, focused); err != nil || len(desk.Sent()) != 0 {
		t.Fatalf("rang before the grace: %v %q", err, desk.Sent())
	}
	s.x.OldestAge = 31 * time.Second
	if err := l.focusNotice(ctx, s, focused); err != nil {
		t.Fatal(err)
	}
	if got := desk.Sent(); len(got) != 1 || !strings.Contains(got[0], "coder") || !strings.Contains(got[0], "w1:p1") || !strings.Contains(got[0], "2 message") {
		t.Fatalf("desktop: %q", got)
	}
	// The same oldest message on the next pass, escalating or not: silent.
	s.w.Reason = "pane focused"
	for _, d := range []Decision{focused, {Action: ActEscalate, Rule: 8, Reason: "pane focused, escalating"}} {
		if err := l.focusNotice(ctx, s, d); err != nil || len(desk.Sent()) != 1 {
			t.Fatalf("same episode rang again: %v %q", err, desk.Sent())
		}
	}
	// A newer message after the agent read the first: a new episode.
	s.o.OldestID = "200-0"
	if err := l.focusNotice(ctx, s, focused); err != nil || len(desk.Sent()) != 2 {
		t.Fatalf("a new oldest message: %v %q", err, desk.Sent())
	}
	// The focus leaves (another decision): the mark goes.
	if err := l.focusNotice(ctx, s, Decision{Action: ActPrompt, Rule: 9, Reason: "ready"}); err != nil {
		t.Fatal(err)
	}
	if marks, _ := b.QuotaMarks(ctx); marks[focusMark("coder")] != "" {
		t.Fatalf("mark kept after the focus left: %v", marks)
	}
	// Off (0) rings nothing.
	l.cfg.Prompt.FocusGrace = 0
	s.w.Reason, s.o.OldestID = "", "300-0"
	if err := l.focusNotice(ctx, s, focused); err != nil || len(desk.Sent()) != 2 {
		t.Fatalf("off rang: %v %q", err, desk.Sent())
	}
	for _, k := range []string{"cmd", "notify"} {
		if n := len(entries(t, r, b, k)); n != 0 {
			t.Fatalf("%d %s entries written: desktop only", n, k)
		}
	}
	// A dispatcher that lost the lease rings nothing.
	l.cfg.Prompt.FocusGrace = 30 * time.Second
	l2 := &loop{b: b, token: "not-the-holder", last: map[string]string{}, cfg: l.cfg}
	if err := l2.focusNotice(ctx, s, focused); !errors.Is(err, bus.ErrNotHolder) || len(desk.Sent()) != 2 {
		t.Fatalf("a non-holder: %v %q", err, desk.Sent())
	}
}
