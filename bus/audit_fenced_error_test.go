package bus

import (
	"context"
	"testing"
	"time"
)

// reviewer-codex: a Lua error does not roll back earlier writes, so an audit
// append that fails after the act's writes must never interrupt the act: with
// {p}:audit broken (not a stream), each fenced act completes and records its
// progress, and a retry repeats nothing. The trail misses the line (best
// effort), the act stands.
func TestFencedActsSurviveABrokenAuditKey(t *testing.T) {
	ctx := context.Background()
	broken := func(b *Bus) {
		t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
		if err := b.r.Set(ctx, AuditKey(b.project), "not a stream", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	b := ciLease(t)
	broken(b)
	if got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); err != nil || got != CISent {
		t.Fatalf("CI hand-off with a broken audit key: %q %v", got, err)
	}
	if got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); err != nil || got != CISame {
		t.Fatalf("its retry: %q %v (the hand-off sent twice?)", got, err)
	}
	if n := len(ciEntries(t, b, "cmd")); n != 2 { // the owner's hand-off and master's copy, once each
		t.Fatalf("%d cmd entries, want 2", n)
	}
	b2 := ciLease(t)
	broken(b2)
	b2.r.HSet(ctx, CIKey(b2.project), "pr:42", ciDigA+"|3|0|om")
	if got, err := b2.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); err != nil || got != CIStopped {
		t.Fatalf("CI stop with a broken audit key: %q %v", got, err)
	}

	b3 := dialDispatch(t)
	broken(b3)
	t.Cleanup(func() { b3.r.Del(ctx, PauseKey(b3.project)) })
	if ok, err := b3.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UnixMilli()
	if wrote, err := b3.FencedQuotaPause(ctx, "tok", "anthropic", "master", until); err != nil || !wrote {
		t.Fatalf("quota pause with a broken audit key: %v %v", wrote, err)
	}
	if ok, err := b3.FencedQuotaRepause(ctx, "tok", "anthropic", "openai", until); err != nil || !ok {
		t.Fatalf("repause with a broken audit key: %v %v", ok, err)
	}

	b4 := ciLease(t)
	broken(b4)
	inReview(t, b4, "t1", ReviewOpts{Branch: "coder/t1", ReviewBase: mBase})
	if got, err := b4.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev", 1); err != nil || got != MergedClosed {
		t.Fatalf("close on merge with a broken audit key: %q %v", got, err)
	}
}
