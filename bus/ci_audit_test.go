package bus

import (
	"context"
	"errors"
	"testing"
)

// audit step 2 (spec §2): the dispatcher's CI hand-off writes its audit line
// inside its fenced script, in the same step: one ci-handoff line when the
// owner's hand-off is written, one (outcome refused) when the loop stops;
// nothing for a step that writes no new part, nor for a refused one.
func TestCIStepAudits(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	if _, err := b.FencedCIStep(ctx, "stale", ciStep(ciDigA, "coder")); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
	if n := auditLen(t, b); n != 0 {
		t.Fatalf("a step without the lease wrote %d audit lines", n)
	}
	if got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); err != nil || got != CISent {
		t.Fatalf("hand-off: %q %v", got, err)
	}
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); got != CISame {
		t.Fatalf("same: %q", got)
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 1 {
		t.Fatalf("%d audit lines after a hand-off and a same step, want 1: %+v", len(es), es)
	}
	e := es[0]
	if e.Action != "ci-handoff" || e.Actor != "dispatch" || e.Target != "pr 42" || e.Outcome != AuditOK || e.Detail != "hand-off 1 of 3 to coder, head aaaaaaaa" {
		t.Fatalf("the hand-off's line %+v", e)
	}
	// The cap reached: the stop is written once, its line refused.
	b2, ctx := ciLease(t), context.Background()
	t.Cleanup(func() { b2.r.Del(ctx, AuditKey(b2.project)) })
	b2.r.HSet(ctx, CIKey(b2.project), "pr:42", ciDigA+"|3|0|om")
	if got, _ := b2.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); got != CIStopped {
		t.Fatalf("stop: %q", got)
	}
	if got, _ := b2.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); got != CIStoppedBefore {
		t.Fatalf("stopped before: %q", got)
	}
	es, _ = b2.AuditEntries(ctx, 0)
	if len(es) != 1 || es[0].Action != "ci-handoff" || es[0].Outcome != AuditRefused || es[0].Detail != "stopped after 3 hand-offs, head bbbbbbbb" {
		t.Fatalf("the stop's lines %+v", es)
	}
	if c, err := b.AuditVerify(ctx); err != nil || !c.OK {
		t.Fatalf("the chain: %+v %v", c, err)
	}
}
