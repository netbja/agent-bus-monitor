package dispatchsvc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// board-drift step 2 (spec §3.1-§3.3) in the merge queue's pass: a review
// request whose branch head is delivered into the base, and was not already
// in its review_base, is closed (or its --more step sent back); an accepted
// request never is; a live derive or a failed pass writes nothing.

const (
	cmReviewBase = "3333333333333333333333333333333333333333"
	cmOther      = "4444444444444444444444444444444444444444"
)

// reviewed puts task, to coder, in review on branch with opts.
func reviewed(t *testing.T, b *bus.Bus, task string, o bus.ReviewOpts) {
	t.Helper()
	ctx := context.Background()
	if _, err := b.Request(ctx, task, "master", "coder", "", "x", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequestOpts(ctx, task, "coder", o); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, b *bus.Bus, task string) (string, *bus.RequestInfo) {
	t.Helper()
	m, err := b.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m[task].State, m[task].Request
}

func TestMergeQueueChoreClosesOnMerge(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	// feat/request-priority's tip is mqHead (mqChore's remote tips).
	reviewed(t, b, "merged", bus.ReviewOpts{Branch: "feat/request-priority", ReviewBase: cmReviewBase})
	reviewed(t, b, "no-base", bus.ReviewOpts{Branch: "feat/request-priority"})
	reviewed(t, b, "gone", bus.ReviewOpts{Branch: "feat/gone", Commit: cmOther, ReviewBase: cmReviewBase}) // auto-deleted: its commit
	reviewed(t, b, "in-base", bus.ReviewOpts{Branch: "feat/old", Commit: cmReviewBase, ReviewBase: cmReviewBase})
	reviewed(t, b, "not-merged", bus.ReviewOpts{Branch: "feat/wip", Commit: mqHead[:39] + "0", ReviewBase: cmReviewBase})
	// Resumed after a review (a multi-step build): accepted, its branch and
	// base kept; never judged, never closed (§3.1).
	resumedHead := "5555555555555555555555555555555555555555"
	reviewed(t, b, "resumed", bus.ReviewOpts{Branch: "feat/resumed", Commit: resumedHead, ReviewBase: cmReviewBase})
	if err := b.AcceptRequest(ctx, "resumed", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "working", "master", "coder", "", "x", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "working", "coder"); err != nil {
		t.Fatal(err)
	}
	var judged []string
	c.inBase = func(_ context.Context, _ string, _ []string, tip string, heads []string) (map[string]bool, error) {
		out := map[string]bool{}
		for _, h := range heads {
			out[h] = tip == cmReviewBase && h == cmReviewBase // only the in-base request's head is in its review_base
		}
		return out, nil
	}
	c.delivered = func(_ context.Context, _ string, _ []string, _ string, heads []string) (map[string]bool, error) {
		judged = append(judged, heads...)
		return map[string]bool{mqHead: true, cmOther: true, cmReviewBase: true, resumedHead: true}, nil
	}
	// A live derive writes nothing.
	c.derive(ctx)
	if s, _ := stateOf(t, b, "merged"); s != "review" {
		t.Fatalf("a live derive closed it: %s", s)
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	s, r := stateOf(t, b, "merged")
	if s != "done" || r.ClosedOnMerge == nil || r.ClosedOnMerge.Head != mqHead || r.ClosedOnMerge.Base != "main" {
		t.Fatalf("merged: %s %+v", s, r)
	}
	if s, r = stateOf(t, b, "gone"); s != "done" || r.ClosedOnMerge.Head != cmOther {
		t.Fatalf("a deleted branch, judged by its commit: %s %+v", s, r)
	}
	for task, want := range map[string]string{"no-base": "review", "in-base": "review", "not-merged": "review", "working": "accepted", "resumed": "accepted"} {
		if s, _ := stateOf(t, b, task); s != want {
			t.Errorf("%s: %s, want %s", task, s, want)
		}
	}
	if slices.Contains(judged, cmReviewBase) || slices.Contains(judged, resumedHead) {
		t.Fatalf("a head already in its review_base, or an accepted request's, was judged: %v", judged)
	}
}

// A pass that fails touches nothing; a read of the base's delivery that
// fails closes nothing.
func TestMergeQueueChoreCloseOnMergeFailures(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	reviewed(t, b, "merged", bus.ReviewOpts{Branch: "feat/request-priority", ReviewBase: cmReviewBase})
	c.delivered = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
		return map[string]bool{mqHead: true}, nil
	}
	gh := c.github
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return nil, errors.New("gh: 502")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, _ := stateOf(t, b, "merged"); s != "review" {
		t.Fatalf("a failed pass closed it: %s", s)
	}
	c.github = gh
	c.delivered = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
		return nil, errors.New("git: busy")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, _ := stateOf(t, b, "merged"); s != "review" {
		t.Fatalf("an unread delivery closed it: %s", s)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" || strings.Contains(rec.Reason, "close") {
		t.Fatalf("the queue's own outcome changed: %+v", rec)
	}
}

// reviewer-codex: with a remote, a branch gone from origin is read from the
// local refs before the recorded commit (§3.2's remote, then local): a
// local tip that moved to an unmerged head keeps the request open; a branch
// gone from both falls back to the commit; a local read that fails judges
// nothing.
func TestMergeQueueChoreCloseOnMergeLocalBeforeCommit(t *testing.T) {
	ctx := context.Background()
	const h1, h2 = "6666666666666666666666666666666666666666", "7777777777777777777777777777777777777777"
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	reviewed(t, b, "moved", bus.ReviewOpts{Branch: "feat/moved", Commit: h1, ReviewBase: cmReviewBase})
	reviewed(t, b, "deleted", bus.ReviewOpts{Branch: "feat/deleted", Commit: h1, ReviewBase: cmReviewBase})
	c.localTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return []mergequeue.Tip{{Branch: "feat/moved", Head: h2}}, mqBaseTip, nil
	}
	c.delivered = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
		return map[string]bool{h1: true}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, _ := stateOf(t, b, "moved"); s != "review" {
		t.Fatalf("closed on the recorded commit despite a newer local tip: %s", s)
	}
	if s, _ := stateOf(t, b, "deleted"); s != "done" {
		t.Fatalf("a branch gone everywhere, its commit merged: %s", s)
	}
	reviewed(t, b, "unread", bus.ReviewOpts{Branch: "feat/unread", Commit: h1, ReviewBase: cmReviewBase})
	c.localTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return nil, "", errors.New("git: busy")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, _ := stateOf(t, b, "unread"); s != "review" {
		t.Fatalf("closed with the local refs unread: %s", s)
	}
}

// foureyes: squash merges leave the branch on origin at its old tip. A
// --more build's next step on the same branch, reviewed at h2 while origin
// still shows step 1's h1 (delivered by content), stays open: the head
// judged is the one reviewed, and a tip that differs from it is not judged
// this pass. Once origin shows h2, h2 is judged.
func TestMergeQueueChoreCloseOnMergeStaleOriginTip(t *testing.T) {
	ctx := context.Background()
	const h1, h2 = "6666666666666666666666666666666666666666", "7777777777777777777777777777777777777777"
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	reviewed(t, b, "step2", bus.ReviewOpts{Branch: "feat/step", Commit: h2, ReviewBase: cmReviewBase, More: true})
	tip := h1
	c.remoteTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return []mergequeue.Tip{{Branch: "feat/request-priority", Head: mqHead}, {Branch: "feat/step", Head: tip}}, mqBaseTip, nil
	}
	merged := map[string]bool{h1: true}
	c.delivered = func(_ context.Context, _ string, _ []string, _ string, heads []string) (map[string]bool, error) {
		return merged, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, r := stateOf(t, b, "step2"); s != "review" || len(r.MergedSteps) != 0 {
		t.Fatalf("closed on origin's stale tip: %s %+v", s, r)
	}
	tip = h2 // pushed; then merged
	merged[h2] = true
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if s, r := stateOf(t, b, "step2"); s != "accepted" || len(r.MergedSteps) != 1 || r.MergedSteps[0] != h2 {
		t.Fatalf("the reviewed head, merged: %s %+v", s, r)
	}
}
