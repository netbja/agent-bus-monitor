package bus

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// board-drift §3 (step 2): request review records the head and the base tip
// it was judged against; FencedRequestMerged closes a review request whose
// branch merged, or sends a --more step back to accepted.

const (
	mHead = "1111111111111111111111111111111111111111"
	mBase = "2222222222222222222222222222222222222222"
)

// inReview is a request to coder, accepted and put in review with opts.
func inReview(t *testing.T, b *Bus, task string, opts ReviewOpts) {
	t.Helper()
	ctx := context.Background()
	if _, err := b.Request(ctx, task, "master", "coder", "", "build it", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequestOpts(ctx, task, "coder", opts); err != nil {
		t.Fatal(err)
	}
}

func boardReq(t *testing.T, b *Bus, task string) BoardEntry {
	t.Helper()
	m, err := b.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m[task]
}

func TestReviewRecordsHeadAndBase(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/t1", Commit: mHead, ReviewBase: mBase, More: true, Note: "step 1"})
	r := boardReq(t, b, "t1").Request
	if r.Branch != "coder/t1" || r.Commit != mHead || r.ReviewBase != mBase || !r.ReviewMore || r.ReviewNote != "step 1" {
		t.Fatalf("recorded %+v", r)
	}
	// Resumed, then in review again with no commit, no base, no --more: a
	// review's facts are its own, never the last one's.
	if err := b.AcceptRequest(ctx, "t1", "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{}); err != nil {
		t.Fatal(err)
	}
	if r = boardReq(t, b, "t1").Request; r.Commit != "" || r.ReviewBase != "" || r.ReviewMore || r.Branch != "coder/t1" {
		t.Fatalf("the second review kept the first's facts: %+v", r)
	}
	for _, bad := range []ReviewOpts{{Commit: "xyz"}, {ReviewBase: "short"}} {
		if err := b.ReviewRequestOpts(ctx, "t1", "coder", bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestRequestMergedCloses(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/t1", ReviewBase: mBase})
	cmds := func() int64 { n, _ := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Result(); return n }
	before := cmds()
	got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev")
	if err != nil || got != MergedClosed {
		t.Fatalf("%q %v", got, err)
	}
	e := boardReq(t, b, "t1")
	if e.State != "done" || e.Request.ClosedOnMerge == nil || e.Request.ClosedOnMerge.Head != mHead || e.Request.ClosedOnMerge.Base != "dev" ||
		e.Request.RespondedAt == 0 || e.Request.ResponseID != "" || e.Request.ClosedUnaccepted != "" {
		t.Fatalf("closed %+v %+v", e, e.Request)
	}
	if cmds() != before {
		t.Fatal("a reply was written: it wakes the sender (Q3: nobody is woken)")
	}
	evs, _ := b.RecentEvents(ctx, 10)
	if !slices.ContainsFunc(evs, func(v TermEvent) bool {
		return v.Kind == EventRequestMerged && v.Agent == "coder" && v.Detail == "t1 coder/t1 "+mHead
	}) {
		t.Fatalf("no request-merged event: %+v", evs)
	}
	// Done: a second call writes nothing.
	if got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev"); err != nil || got != MergedNone {
		t.Fatalf("again: %q %v", got, err)
	}
}

// --more: the request goes back to accepted, the step recorded, its branch,
// commit and flag cleared; the last step (no --more) closes it.
func TestRequestMergedStep(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/t1-a", Commit: mHead, ReviewBase: mBase, More: true})
	got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1-a", mHead, "dev")
	if err != nil || got != MergedStep {
		t.Fatalf("%q %v", got, err)
	}
	e := boardReq(t, b, "t1")
	if e.State != "accepted" || e.Request.Branch != "" || e.Request.Commit != "" || e.Request.ReviewMore || e.Request.ReviewBase != "" ||
		!slices.Equal(e.Request.MergedSteps, []string{mHead}) || e.Request.RespondedAt != 0 {
		t.Fatalf("step %+v %+v", e, e.Request)
	}
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/t1-b", ReviewBase: mBase}); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1-b", mBase, "dev"); got != MergedClosed {
		t.Fatalf("the last step: %q", got)
	}
	if e = boardReq(t, b, "t1"); e.State != "done" || !slices.Equal(e.Request.MergedSteps, []string{mHead}) {
		t.Fatalf("closed %+v", e.Request)
	}
}

// What changed since the pass read it writes nothing: another state (an
// accepted request is never closed on merge), another branch; no lease, an
// error.
func TestRequestMergedRefusals(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/t1", ReviewBase: mBase})
	if _, err := b.FencedRequestMerged(ctx, "other", "t1", "coder/t1", mHead, "dev"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("not the holder: %v", err)
	}
	if got, _ := b.FencedRequestMerged(ctx, "tok", "t1", "coder/other", mHead, "dev"); got != MergedNone {
		t.Fatalf("another branch: %q", got)
	}
	if err := b.AcceptRequest(ctx, "t1", "coder"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.FencedRequestMerged(ctx, "tok", "t1", "coder/t1", mHead, "dev"); got != MergedNone {
		t.Fatalf("accepted: %q", got)
	}
	if got, _ := b.FencedRequestMerged(ctx, "tok", "nope", "coder/t1", mHead, "dev"); got != MergedNone {
		t.Fatalf("unknown: %q", got)
	}
	if e := boardReq(t, b, "t1"); e.State != "accepted" {
		t.Fatalf("written: %+v", e)
	}
}
