package bus

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func workBranchOf(t *testing.T, b *Bus, task string) (string, string) {
	t.Helper()
	board, err := b.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e := board[task]
	if e.Request == nil {
		t.Fatalf("%s: no request", task)
	}
	return e.State, e.Request.WorkBranch
}

// request accept --branch records the work branch (code-intel §7) in its
// own field; review's branch is untouched. A branch another agent's open
// request declares, as work_branch or as review's branch, is refused and
// the accept leaves nothing written; a done request holds no branch, and
// the agent's own other request does not block it.
func TestAcceptRequestBranch(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, r := range [][2]string{{"a", "coder"}, {"b", "coder2"}, {"c", "coder2"}, {"d", "coder"}, {"e", "coder2"}, {"f", "coder2"}} {
		if _, err := b.Request(ctx, r[0], "master", r[1], "", "body", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AcceptRequestBranch(ctx, "a", "coder", 0, "coder/x"); err != nil {
		t.Fatal(err)
	}
	if st, wb := workBranchOf(t, b, "a"); st != "accepted" || wb != "coder/x" {
		t.Fatalf("a: %s %q", st, wb)
	}
	board, _ := b.Board(ctx)
	if board["a"].Request.Branch != "" {
		t.Fatalf("review's branch written: %q", board["a"].Request.Branch)
	}
	err := b.AcceptRequestBranch(ctx, "b", "coder2", 0, "coder/x")
	if !errors.Is(err, ErrRequestRefused) || err.Error() != "branch coder/x belongs to coder's request a" {
		t.Fatalf("taken: %v", err)
	}
	if st, wb := workBranchOf(t, b, "b"); st != "requested" || wb != "" {
		t.Fatalf("a refused accept wrote: %s %q", st, wb)
	}

	// review's branch is a claim too, and so is a blocked request's.
	if err := b.AcceptRequest(ctx, "d", "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequest(ctx, "d", "coder", "coder/rev", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestBranch(ctx, "b", "coder2", 0, "coder/rev"); err == nil || err.Error() != "branch coder/rev belongs to coder's request d" {
		t.Fatalf("review branch: %v", err)
	}
	if err := b.BlockRequest(ctx, "a", "coder", "waiting"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestBranch(ctx, "b", "coder2", 0, "coder/x"); err == nil {
		t.Fatal("a blocked request's branch was taken")
	}

	// done: the branch is free again.
	if err := b.AcceptRequest(ctx, "a", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "a", "coder", "done"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestBranch(ctx, "b", "coder2", 0, "coder/x"); err != nil {
		t.Fatalf("after done: %v", err)
	}
	// the agent's own other request does not block it.
	if err := b.AcceptRequestBranch(ctx, "c", "coder2", 0, "coder/x"); err != nil {
		t.Fatalf("own request: %v", err)
	}
	if err := b.AcceptRequestBranch(ctx, "e", "coder2", 0, "-bad"); err == nil {
		t.Fatal("an invalid branch was accepted")
	}
	if st, _ := workBranchOf(t, b, "e"); st != "requested" {
		t.Fatalf("an invalid branch accepted the request: %s", st)
	}
	// accept without --branch keeps a work branch declared before.
	if err := b.AcceptRequest(ctx, "c", "coder2"); err != nil {
		t.Fatal(err)
	}
	if _, wb := workBranchOf(t, b, "c"); wb != "coder/x" {
		t.Fatalf("a plain accept cleared it: %q", wb)
	}
}

// request branch records the work branch later: by the target only, while
// the request is accepted, under the same one-owner rule.
func TestSetWorkBranch(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, r := range [][2]string{{"a", "coder"}, {"b", "coder2"}} {
		if _, err := b.Request(ctx, r[0], "master", r[1], "", "body", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.SetWorkBranch(ctx, "a", "coder", "coder/a"); !errors.Is(err, ErrRequestRefused) || err.Error() != "only an accepted request records a work branch" {
		t.Fatalf("not accepted: %v", err)
	}
	if err := b.AcceptRequest(ctx, "a", "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetWorkBranch(ctx, "a", "master", "coder/a"); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("not the target: %v", err)
	}
	if err := b.SetWorkBranch(ctx, "a", "coder", "coder/a"); err != nil {
		t.Fatal(err)
	}
	if st, wb := workBranchOf(t, b, "a"); st != "accepted" || wb != "coder/a" {
		t.Fatalf("%s %q", st, wb)
	}
	if err := b.AcceptRequest(ctx, "b", "coder2"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetWorkBranch(ctx, "b", "coder2", "coder/a"); err == nil || err.Error() != "branch coder/a belongs to coder's request a" {
		t.Fatalf("taken: %v", err)
	}
	if err := b.SetWorkBranch(ctx, "a", "coder", "coder/a2"); err != nil {
		t.Fatalf("a new one of its own: %v", err)
	}
	if err := b.SetWorkBranch(ctx, "b", "coder2", "coder/a"); err != nil {
		t.Fatalf("the old one, released: %v", err)
	}
	if err := b.ReviewRequest(ctx, "a", "coder", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.SetWorkBranch(ctx, "a", "coder", "coder/a3"); err == nil {
		t.Fatal("recorded in review")
	}
	if err := b.SetWorkBranch(ctx, "b", "coder2", "bad..name"); err == nil {
		t.Fatal("an invalid branch was recorded")
	}
}

// Two agents claiming one branch at once: one owner.
func TestWorkBranchConcurrentClaims(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	agents := []string{"coder", "coder2", "docs", "architect"}
	for _, a := range agents {
		if _, err := b.Request(ctx, "t-"+a, "master", a, "", "body", 0); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, len(agents))
	for i, a := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = b.AcceptRequestBranch(ctx, "t-"+a, a, 0, "shared/x")
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d owners: %v", won, errs)
	}
}
