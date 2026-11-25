package bus

import (
	"context"
	"errors"
	"testing"
)

// audit step 2 (master's answer): merge is recorded at the merge queue's
// close-on-merge, in its fenced script: actor dispatch, the branch, head and
// base; a second call (nothing to close) and a call without the lease write
// none. agentbus never merges: the forge does, this is the merge seen.
func TestRequestMergedAudits(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/t1", ReviewBase: mBase})
	if _, err := b.FencedRequestMerged(ctx, "stale", "t1", "coder/t1", mHead, "dev", 1); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
	if got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev", 1); err != nil || got != MergedClosed {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev", 1); got != MergedNone {
		t.Fatalf("second: %q", got)
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 1 {
		t.Fatalf("%d lines, want 1: %+v", len(es), es)
	}
	e := es[0]
	if e.Action != "merge" || e.Actor != "dispatch" || e.Target != "coder/t1" || e.Outcome != AuditOK ||
		e.Detail != "head 11111111 into dev, request t1 closed" {
		t.Fatalf("the merge's line %+v", e)
	}
	// A --more step: the merge is recorded the same, the request back to accepted.
	inReview(t, b, "t2", ReviewOpts{Branch: "coder/t2", ReviewBase: mBase, More: true})
	if got, _ := b.FencedRequestMerged(ctx, "tok", "t2", "coder/t2", mHead, "dev", 1); got != MergedStep {
		t.Fatalf("step: %q", got)
	}
	if es, _ = b.AuditEntries(ctx, 0); len(es) != 2 || es[0].Detail != "head 11111111 into dev, request t2 step" {
		t.Fatalf("the step's line %+v", es)
	}
}
