package main

import (
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

const (
	rqHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rqHeadB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func (w *stallWorld) verdict(subject, reviewer, head string) {
	w.t.Helper()
	if _, err := w.b.AppendVerdict(context.Background(), bus.Verdict{Subject: subject, Author: "coder", Reviewer: reviewer,
		Decision: "approve", Head: head}); err != nil {
		w.t.Fatal(err)
	}
}

// A subject approved by one reviewer on its new head, the other's approve on
// an older head: half-approved, master woken once; recorded done in the plan,
// it is history.
func TestReviewQueueReadsAHalfApproval(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if out := w.run(w.s.reviewQueue); out.Status != health.OK || out.Detail != "no review owed" {
		t.Fatalf("nothing owed: %+v", out)
	}
	w.verdict("t1", "reviewer-codex", rqHeadA)
	w.verdict("t1", "foureyes", rqHeadB)
	out := w.run(w.s.reviewQueue)
	if out.Status != health.Warn || !strings.Contains(out.Detail, "t1 approved by foureyes, not by reviewer-codex") {
		t.Fatalf("half-approved: %+v", out)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "review-queue"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 || !strings.Contains(got[0], "t1 approved by foureyes, not by reviewer-codex") {
		t.Fatalf("master woken %q", got)
	}
	if err := w.b.PlanDone(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.reviewQueue); out.Status != health.OK {
		t.Fatalf("plan done: %+v", out)
	}
}

// A gate whose challenge the reviewer has not read is the dispatcher's
// (messages wait: open work); once drained and left open, it is owed.
func TestReviewQueueReadsAnOwedGate(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if err := w.b.OpenChallenge(ctx, "coder", "r1", bus.FormatGateMeta("master", "focus-notice", "review it")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdChallenge, "r1", "review it"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.reviewQueue); out.Status != health.OK || !strings.Contains(out.Detail, "messages waiting for coder") {
		t.Fatalf("unread challenge: %+v", out)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.reviewQueue); out.Status != health.Warn || !strings.Contains(out.Detail, "coder owes focus-notice (") {
		t.Fatalf("read and left open: %+v", out)
	}
	w.env["AGENT_BUS_STALL_AFTER"] = "0"
	if out := w.run(w.s.reviewQueue); out.Status != health.Unknown {
		t.Fatalf("off: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "review-queue"); err != nil {
		t.Fatal(err)
	}
	if got := w.dispatchDirectives("master"); len(got) != 0 {
		t.Fatalf("an off check woke master: %q", got)
	}
}

// A gate whose challenge was trimmed is never owed: a group skips an entry
// trimmed before it read it, so even a cursor past it proves nothing; it is
// not judged (reviewer-codex).
func TestReviewQueueTrimmedChallenge(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if err := w.b.OpenChallenge(ctx, "coder", "r1", bus.FormatGateMeta("master", "focus-notice", "review it")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdChallenge, "r1", "review it"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Purge(ctx, []string{"cmd"}); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.reviewQueue); out.Status != health.Unknown || !strings.Contains(out.Detail, "not judged: coder's gate r1") {
		t.Fatalf("unread, then trimmed: %+v", out)
	}
	if _, err := w.b.CmdNoWake(ctx, "master", "coder", bus.CmdDirective, "", "note for coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.reviewQueue); out.Status != health.Unknown || !strings.Contains(out.Detail, "not judged: coder's gate r1") {
		t.Fatalf("the cursor passed the trimmed challenge unread: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "review-queue"); err != nil {
		t.Fatal(err)
	}
	if got := w.dispatchDirectives("master"); len(got) != 0 {
		t.Fatalf("an unjudged gate woke master: %q", got)
	}
}
