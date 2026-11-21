package bus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// wipWorld: requests for peer (and one for other), each one sent; accepted
// lists the ones peer accepts first, without a limit.
func wipWorld(t *testing.T, accepted ...string) (*Bus, context.Context) {
	t.Helper()
	b := dialTest(t)
	ctx := context.Background()
	for _, task := range []string{"a", "b", "c", "d"} {
		if _, err := b.Request(ctx, task, "master", "peer", "", "do "+task, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Request(ctx, "o", "master", "other", "", "do o", 0); err != nil {
		t.Fatal(err)
	}
	for _, task := range accepted {
		if err := b.AcceptRequest(ctx, task, "peer"); err != nil {
			t.Fatal(err)
		}
	}
	return b, ctx
}

func stateOf(t *testing.T, b *Bus, ctx context.Context, task string) string {
	t.Helper()
	board, err := b.Board(ctx)
	e, ok := board[task]
	if err != nil || !ok {
		t.Fatalf("%s: %v %v", task, ok, err)
	}
	return e.State
}

// wip-limit §3.3 (step 1): a first accept with the limit's count of other
// accepted requests is refused, naming them; under it, it passes.
func TestWIPLimitRefusesAFirstAccept(t *testing.T) {
	b, ctx := wipWorld(t, "b", "a")
	err := b.AcceptRequestLimit(ctx, "c", "peer", 2)
	want := "wip limit reached for peer (2 of 2 accepted: a, b): finish one, or mark it review (agentbus request review <task>) or blocked; then accept c"
	if !errors.Is(err, ErrRequestRefused) || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	if s := stateOf(t, b, ctx, "c"); s != "requested" {
		t.Fatalf("c is %s after the refusal", s)
	}
	if err := b.AcceptRequestLimit(ctx, "c", "peer", 3); err != nil {
		t.Fatalf("limit 3: %v", err)
	}
	if err := b.AcceptRequestLimit(ctx, "d", "peer", 0); err != nil {
		t.Fatalf("0 is no limit: %v", err)
	}
}

// The refusal names the held requests sorted, whatever the board's order.
func TestWIPLimitNamesThemSorted(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, task := range []string{"zz", "mm", "aa", "new"} {
		if _, err := b.Request(ctx, task, "master", "peer", "", "do "+task, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []string{"zz", "mm", "aa"} {
		if err := b.AcceptRequest(ctx, task, "peer"); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AcceptRequestLimit(ctx, "new", "peer", 3); err == nil || !strings.Contains(err.Error(), "(3 of 3 accepted: aa, mm, zz)") {
		t.Fatalf("%v", err)
	}
}

// review and blocked (Q1, the person's answer: blocked does not count) are
// not in flight, nor are another agent's accepted requests.
func TestWIPLimitCountsOnlyTheAgentsAccepted(t *testing.T) {
	b, ctx := wipWorld(t, "a", "b")
	if err := b.AcceptRequest(ctx, "o", "other"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequest(ctx, "a", "peer", "feat/a", "step 1 in review"); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "b", "peer", "waits on the person"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestLimit(ctx, "c", "peer", 1); err != nil {
		t.Fatalf("review, blocked and other's counted: %v", err)
	}
	if err := b.AcceptRequestLimit(ctx, "d", "peer", 1); !errors.Is(err, ErrRequestRefused) || !strings.Contains(err.Error(), "(1 of 1 accepted: c)") {
		t.Fatalf("d: %v", err)
	}
}

// A resume is never refused (§3.3): back from review or blocked, even above
// the limit. So an agent can hold more than the limit through resumes
// (foureyes's note); a first accept is refused until it is under again.
func TestWIPLimitNeverRefusesAResume(t *testing.T) {
	b, ctx := wipWorld(t)
	for _, task := range []string{"a", "b"} {
		if err := b.AcceptRequestLimit(ctx, task, "peer", 1); err != nil {
			t.Fatal(err)
		}
		if err := b.ReviewRequest(ctx, task, "peer", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.BlockRequest(ctx, "a", "peer", "waits"); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"a", "b"} { // a from blocked, b from review
		if err := b.AcceptRequestLimit(ctx, task, "peer", 1); err != nil {
			t.Fatalf("resume %s: %v", task, err)
		}
	}
	if err := b.AcceptRequestLimit(ctx, "c", "peer", 1); !errors.Is(err, ErrRequestRefused) || !strings.Contains(err.Error(), "(2 of 1 accepted: a, b)") {
		t.Fatalf("c above the limit: %v", err)
	}
}

// Only the person's high priority passes (Q4): priority high with
// priority_by. The plan file's high (no priority_by, master's) does not, nor
// does low.
func TestWIPLimitThePersonsHighPriorityPasses(t *testing.T) {
	b, ctx := wipWorld(t, "a")
	if _, err := b.SetRequestPriority(ctx, "b", PriorityHigh, "person"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestLimit(ctx, "b", "peer", 1); err != nil {
		t.Fatalf("the person's high: %v", err)
	}
	// The plan file's high: priority without priority_by.
	raw, _ := b.r.HGet(ctx, BoardKey(b.project), "c").Result()
	raw = strings.Replace(raw, `"request":{`, `"request":{"priority":"high",`, 1)
	if err := b.r.HSet(ctx, BoardKey(b.project), "c", raw).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestLimit(ctx, "c", "peer", 1); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("a high without the person passed: %v", err)
	}
	if _, err := b.SetRequestPriority(ctx, "d", PriorityLow, "person"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequestLimit(ctx, "d", "peer", 1); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("low passed: %v", err)
	}
}

// The count is read in the script that writes: two first accepts at limit-1
// at once, one passes and one is refused.
func TestWIPLimitConcurrentFirstAccepts(t *testing.T) {
	for range 10 {
		b, ctx := wipWorld(t, "a")
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, task := range []string{"b", "c"} {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = b.AcceptRequestLimit(ctx, task, "peer", 2) }()
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("both or neither passed: %v, %v", errs[0], errs[1])
		}
	}
}

// review (§3.2): from accepted only, by the target only; it writes the
// branch and the note, and no stream entry. done and block pass from it.
func TestRequestReview(t *testing.T) {
	b, ctx := wipWorld(t, "a", "b")
	before, _ := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Result()
	if err := b.ReviewRequest(ctx, "a", "peer", "feat/a", "step 1 in review"); err != nil {
		t.Fatal(err)
	}
	if after, _ := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Result(); after != before {
		t.Fatalf("review wrote %d stream entries", after-before)
	}
	board, _ := b.Board(ctx)
	e := board["a"]
	if e.State != "review" || e.Request.Branch != "feat/a" || e.Request.ReviewNote != "step 1 in review" {
		t.Fatalf("a: %s %+v", e.State, e.Request)
	}
	for _, c := range []struct{ task, agent, why string }{
		{"c", "peer", "requested"},
		{"b", "other", "another agent"},
		{"a", "peer", "already in review"},
	} {
		if err := b.ReviewRequest(ctx, c.task, c.agent, "", ""); !errors.Is(err, ErrRequestRefused) {
			t.Errorf("%s: %v", c.why, err)
		}
	}
	if _, err := b.CompleteRequest(ctx, "b", "peer", "done"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequest(ctx, "b", "peer", "", ""); !errors.Is(err, ErrRequestRefused) {
		t.Errorf("review from done: %v", err)
	}
	if err := b.BlockRequest(ctx, "a", "peer", "waits on the person"); err != nil || stateOf(t, b, ctx, "a") != "blocked" {
		t.Fatalf("block from review: %v", err)
	}
	if err := b.AcceptRequest(ctx, "a", "peer"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequest(ctx, "a", "peer", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "a", "peer", "merged"); err != nil || stateOf(t, b, ctx, "a") != "done" {
		t.Fatalf("done from review: %v", err)
	}
}

// NextRequest: a review request does not hold the agent; finish first still
// names an accepted one.
func TestNextRequestSkipsReview(t *testing.T) {
	r := func(state string) BoardEntry {
		return BoardEntry{State: state, Request: &RequestInfo{Target: "peer", Availability: "retained"}}
	}
	board := map[string]BoardEntry{"a": r("review"), "b": r("requested")}
	if task, why := NextRequest(board, "peer"); task != "b" || why != "" {
		t.Fatalf("got %s %q", task, why)
	}
	board["c"] = r("accepted")
	if task, why := NextRequest(board, "peer"); task != "c" || why != "finish first" {
		t.Fatalf("got %s %q", task, why)
	}
}
