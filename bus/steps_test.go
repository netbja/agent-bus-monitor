package bus

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// sibling-enforce §3 on the board: one open step per request, judged by the
// CLI and applied by the transition script.

const (
	sHeadB = "3333333333333333333333333333333333333333"
	sHeadC = "4444444444444444444444444444444444444444"
	sBase2 = "5555555555555555555555555555555555555555"
)

func openSteps(t *testing.T, b *Bus, task string) []string {
	t.Helper()
	var out []string
	for _, s := range boardReq(t, b, task).Request.OpenSteps {
		out = append(out, s.Branch+"@"+s.Commit[:4])
	}
	return out
}

func resume(t *testing.T, b *Bus, task string) {
	t.Helper()
	if err := b.AcceptRequest(context.Background(), task, "coder"); err != nil {
		t.Fatal(err)
	}
}

// judgedB is B delivered, as the CLI passes it, at review n.
func judgedB(n int) StepsJudged {
	return StepsJudged{N: n, Merged: []StepMerged{{Branch: "coder/b", Head: sHeadB, Base: sBase2}}}
}

// Rule 1: a review of another branch is refused while B is open and not
// judged merged; a review of B itself passes; B judged merged passes, leaves
// the list, its head recorded once; a review_n changed since the read is
// refused and writes nothing; --branch is required while a step is open.
func TestOpenStepRefusesASiblingReview(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true})
	if got := openSteps(t, b, "t1"); !slices.Equal(got, []string{"coder/b@3333"}) {
		t.Fatalf("listed %v", got)
	}
	resume(t, b, "t1")
	err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/c", Commit: sHeadC, ReviewBase: mBase, More: true})
	if !errors.Is(err, ErrRequestRefused) || err.Error() != "step coder/b of t1 is not merged yet: sibling steps run in series (merge-conflicts §3); take another task, or ask master for agentbus request parallel t1" {
		t.Fatalf("a sibling review: %v", err)
	}
	if e := boardReq(t, b, "t1"); e.State != "accepted" || e.Request.Branch != "coder/b" || e.Request.ReviewN != 1 {
		t.Fatalf("the refusal wrote: %+v", e.Request)
	}
	err = b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Commit: sHeadC, ReviewBase: mBase})
	if !errors.Is(err, ErrRequestRefused) || !strings.HasPrefix(err.Error(), "a step is open on coder/b: name the branch (--branch coder/b for coder/b itself)") {
		t.Fatalf("no --branch: %v", err)
	}
	// B itself (a fix): passes, its new head listed.
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/b", Commit: sHeadC, ReviewBase: mBase, More: true}); err != nil {
		t.Fatal(err)
	}
	if got := openSteps(t, b, "t1"); !slices.Equal(got, []string{"coder/b@4444"}) {
		t.Fatalf("after a fix %v", got)
	}
	resume(t, b, "t1")
	// A judgement read at an older review: refused, nothing written.
	err = b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/c", Commit: sHeadC, ReviewBase: mBase, More: true, Steps: judgedB(1)})
	if !errors.Is(err, ErrRequestRefused) || !strings.HasPrefix(err.Error(), "request changed since its steps were judged") {
		t.Fatalf("a stale judgement: %v", err)
	}
	// Judged at the review it read: B leaves, C is listed.
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/c", Commit: sHeadC, ReviewBase: mBase, More: true, Steps: judgedB(2)}); err != nil {
		t.Fatal(err)
	}
	r := boardReq(t, b, "t1").Request
	if got := openSteps(t, b, "t1"); !slices.Equal(got, []string{"coder/c@4444"}) || !slices.Equal(r.MergedSteps, []string{sHeadB}) || r.Branch != "coder/c" {
		t.Fatalf("after B merged: open %v merged %v branch %q", got, r.MergedSteps, r.Branch)
	}
}

// A base nobody chose (no review_base) lists nothing; a last review (no
// --more) of a listed branch takes it off the list: its merge closes.
func TestOpenStepNeedsABaseAndMore(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, More: true})
	if got := openSteps(t, b, "t1"); got != nil {
		t.Fatalf("no base, listed %v", got)
	}
	resume(t, b, "t1")
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true}); err != nil {
		t.Fatal(err)
	}
	resume(t, b, "t1")
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase}); err != nil {
		t.Fatal(err)
	}
	if got := openSteps(t, b, "t1"); got != nil {
		t.Fatalf("a last review stays listed: %v", got)
	}
	if got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/b", sHeadB, "dev", 3); err != nil || got != MergedClosed {
		t.Fatalf("its merge: %q %v", got, err)
	}
}

// The target's done waits for every listed step, parallel or not; the
// steps judged merged, it passes. The sender's own done before an accept is
// unchanged.
func TestOpenStepHoldsDone(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true})
	resume(t, b, "t1")
	if _, err := b.SetRequestParallel(ctx, "t1", "master", "no common file", false, false); err != nil {
		t.Fatal(err)
	}
	_, err := b.CompleteRequest(ctx, "t1", "coder", "all done")
	if !errors.Is(err, ErrRequestRefused) || err.Error() != "step coder/b of t1 is not merged yet: done once it merges, or ask master to abandon it" {
		t.Fatalf("done with B listed: %v", err)
	}
	if _, err := b.CompleteRequestSteps(ctx, "t1", "coder", "all done", "", "", judgedB(1)); err != nil {
		t.Fatal(err)
	}
	if e := boardReq(t, b, "t1"); e.State != "done" || e.Request.OpenSteps != nil || !slices.Equal(e.Request.MergedSteps, []string{sHeadB}) {
		t.Fatalf("done %+v", e.Request)
	}
}

// parallel: the sender or the person sets it, the target never, a done
// request never, a reason always. While set, a sibling review passes and
// both are listed; --off forgets neither: the next review is refused on B.
func TestParallelLetsSiblingsThrough(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true})
	resume(t, b, "t1")
	for _, c := range []struct {
		by, reason string
		person     bool
		want       string
	}{
		{"coder", "mine", false, "only the request's sender or the person may change its steps (the target never)"},
		{"foureyes", "not mine", false, "only the request's sender or the person may"},
		{"master", "", false, "a reason is required"},
		{"master", "two\nlines", false, "the reason is one line"},
	} {
		if _, err := b.SetRequestParallel(ctx, "t1", c.by, c.reason, false, c.person); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s %q: %v", c.by, c.reason, err)
		}
	}
	if changed, err := b.SetRequestParallel(ctx, "t1", "charlie", "b and c share no file", false, true); err != nil || !changed {
		t.Fatalf("the person: %v %v", changed, err)
	}
	if p := boardReq(t, b, "t1").Request.Parallel; p == nil || p.By != "charlie" || p.Reason != "b and c share no file" || p.At == 0 {
		t.Fatalf("parallel %+v", p)
	}
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/c", Commit: sHeadC, ReviewBase: mBase, More: true}); err != nil {
		t.Fatal(err)
	}
	if got := openSteps(t, b, "t1"); !slices.Equal(got, []string{"coder/b@3333", "coder/c@4444"}) {
		t.Fatalf("both listed: %v", got)
	}
	resume(t, b, "t1")
	if changed, err := b.SetRequestParallel(ctx, "t1", "master", "", true, false); err != nil || !changed {
		t.Fatalf("off: %v %v", changed, err)
	}
	if got := openSteps(t, b, "t1"); len(got) != 2 {
		t.Fatalf("off forgot a step: %v", got)
	}
	// C judged merged, B not: D is refused on B.
	judgedC := StepsJudged{N: 2, Merged: []StepMerged{{Branch: "coder/c", Head: sHeadC, Base: sBase2}}}
	err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/d", Commit: sHeadC, ReviewBase: mBase, More: true, Steps: judgedC})
	if !errors.Is(err, ErrRequestRefused) || !strings.HasPrefix(err.Error(), "step coder/b of t1 is not merged yet") {
		t.Fatalf("after off: %v", err)
	}
	if got := openSteps(t, b, "t1"); len(got) != 2 {
		t.Fatalf("a refused review wrote: %v", got)
	}
	// A done request: refused.
	if _, err := b.CompleteRequestSteps(ctx, "t1", "coder", "x", "", "", StepsJudged{N: 2, Merged: append(judgedB(2).Merged, judgedC.Merged...)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestParallel(ctx, "t1", "master", "late", false, false); !errors.Is(err, ErrRequestRefused) || err.Error() != "request already completed" {
		t.Fatalf("on a done request: %v", err)
	}
}

// Under parallel, a last review's merge with a step still listed sends the
// request back to accepted (MergedOpen), never done; the done then waits on B.
func TestLastReviewMergeKeepsAListedStepOpen(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true})
	resume(t, b, "t1")
	if _, err := b.SetRequestParallel(ctx, "t1", "master", "no common file", false, false); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/d", Commit: sHeadC, ReviewBase: mBase}); err != nil {
		t.Fatal(err)
	}
	got, err := b.FencedRequestMerged(ctx, "tok", "t1", "coder/d", sHeadC, "dev", 2)
	if err != nil || got != MergedOpen {
		t.Fatalf("%q %v", got, err)
	}
	e := boardReq(t, b, "t1")
	if e.State != "accepted" || e.Request.Branch != "" || !slices.Equal(e.Request.MergedSteps, []string{sHeadC}) || len(e.Request.OpenSteps) != 1 {
		t.Fatalf("after the last merge %+v", e.Request)
	}
	evs, _ := b.RecentEvents(ctx, 10)
	if !slices.ContainsFunc(evs, func(v TermEvent) bool {
		return v.Kind == EventRequestMerged && v.Detail == "t1 coder/d "+sHeadC+" open coder/b"
	}) {
		t.Fatalf("no request-merged event naming the open step: %+v", evs)
	}
	if _, err := b.CompleteRequest(ctx, "t1", "coder", "done"); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("done with B listed: %v", err)
	}
}

// Rule 2: an open step abandoned by the sender or the person, never the
// target: it leaves the list, goes to abandoned_steps, the recorded
// branch's facts cleared, review back to accepted.
func TestAbandonStep(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	inReview(t, b, "t1", ReviewOpts{Branch: "coder/b", Commit: sHeadB, ReviewBase: mBase, More: true})
	for _, c := range []struct {
		by, branch, reason string
		want               string
	}{
		{"coder", "coder/b", "closed", "only the request's sender or the person may"},
		{"master", "coder/b", "", "a reason is required"},
		{"master", "coder/x", "closed", "no such open step on this request"},
	} {
		if _, err := b.AbandonStep(ctx, "t1", c.branch, c.by, c.reason, false); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	head, err := b.AbandonStep(ctx, "t1", "coder/b", "master", "PR 999 closed unmerged", false)
	if err != nil || head != sHeadB {
		t.Fatalf("%q %v", head, err)
	}
	e := boardReq(t, b, "t1")
	if e.State != "accepted" || e.Request.OpenSteps != nil || e.Request.Branch != "" || e.Request.Commit != "" || e.Request.ReviewMore ||
		len(e.Request.AbandonedSteps) != 1 || e.Request.AbandonedSteps[0] != (AbandonedStep{Branch: "coder/b", Head: sHeadB, Reason: "PR 999 closed unmerged", By: "master", At: e.Request.AbandonedSteps[0].At}) ||
		e.Request.AbandonedSteps[0].At == 0 {
		t.Fatalf("abandoned %+v %+v", e, e.Request)
	}
	// Now a sibling review passes.
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", ReviewOpts{Branch: "coder/c", Commit: sHeadC, ReviewBase: mBase, More: true}); err != nil {
		t.Fatal(err)
	}
	// The person abandons too.
	if _, err := b.AbandonStep(ctx, "t1", "coder/c", "charlie", "superseded", true); err != nil {
		t.Fatal(err)
	}
}
