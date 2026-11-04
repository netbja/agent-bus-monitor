package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func boardRaw(t *testing.T, b *Bus, task string) string {
	t.Helper()
	raw, err := b.r.HGet(context.Background(), BoardKey(b.project), task).Result()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requestOf(t *testing.T, b *Bus, task string) (BoardEntry, *RequestInfo) {
	t.Helper()
	board, err := b.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e := board[task]
	if e.Request == nil {
		t.Fatalf("%s is no request: %+v", task, e)
	}
	return e, e.Request
}

func TestSetRequestPriority(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "rq", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	before, _ := requestOf(t, b, "rq")
	t0, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := b.SetRequestPriority(ctx, "rq", "high", "charlie")
	if err != nil || !changed {
		t.Fatalf("set high = %v, %v", changed, err)
	}
	e, r := requestOf(t, b, "rq")
	if r.Priority != "high" || r.PriorityBy != "charlie" || r.PriorityAt < t0.UnixMilli() || r.PriorityAt > t0.Add(time.Minute).UnixMilli() {
		t.Fatalf("after high: %+v", r)
	}
	if e.Updated != before.Updated {
		t.Fatalf("a priority moved updated: %d -> %d", before.Updated, e.Updated)
	}
	raw := boardRaw(t, b, "rq")
	if changed, err := b.SetRequestPriority(ctx, "rq", "high", "someone"); err != nil || changed {
		t.Fatalf("same level again = %v, %v", changed, err)
	}
	if boardRaw(t, b, "rq") != raw {
		t.Fatal("the same level rewrote the entry")
	}
	if changed, err := b.SetRequestPriority(ctx, "rq", "low", "charlie"); err != nil || !changed {
		t.Fatalf("set low = %v, %v", changed, err)
	}
	if _, r := requestOf(t, b, "rq"); r.Priority != "low" {
		t.Fatalf("after low: %+v", r)
	}
	if changed, err := b.SetRequestPriority(ctx, "rq", "normal", "charlie"); err != nil || !changed {
		t.Fatalf("set normal = %v, %v", changed, err)
	}
	if _, r := requestOf(t, b, "rq"); r.Priority != "" || r.PriorityBy != "" || r.PriorityAt != 0 {
		t.Fatalf("normal kept fields: %+v", r)
	}
	if raw := boardRaw(t, b, "rq"); strings.Contains(raw, "priority") {
		t.Fatalf("normal stored: %s", raw)
	}
	if changed, err := b.SetRequestPriority(ctx, "rq", "normal", "charlie"); err != nil || changed {
		t.Fatalf("normal on normal = %v, %v", changed, err)
	}
}

func TestSetRequestPriorityRefusals(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.SetRequestPriority(ctx, "nope", "high", "charlie"); !errors.Is(err, ErrRequestRefused) || err.Error() != "unknown request" {
		t.Fatalf("unknown: %v", err)
	}
	if err := b.BoardClaim(ctx, "plain", "coder", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "plain", "high", "charlie"); !errors.Is(err, ErrRequestRefused) || err.Error() != "board task is not a tracked request" {
		t.Fatalf("untracked: %v", err)
	}
	if _, err := b.Request(ctx, "rq", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "rq", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "rq", "coder", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "rq", "high", "charlie"); !errors.Is(err, ErrRequestRefused) || err.Error() != "request already completed" {
		t.Fatalf("done: %v", err)
	}
	for _, bad := range [][2]string{{"urgent", "charlie"}, {"high", "Not A Name"}, {"high", ""}} {
		if _, err := b.SetRequestPriority(ctx, "rq", bad[0], bad[1]); err == nil {
			t.Fatalf("level %q by %q accepted", bad[0], bad[1])
		}
	}
}

// The level survives every other board writer: each is a cjson round trip.
func TestRequestPrioritySurvivesTheBoardWriters(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "rq", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "rq", "high", "charlie"); err != nil {
		t.Fatal(err)
	}
	keeps := func(step string) {
		t.Helper()
		if _, r := requestOf(t, b, "rq"); r.Priority != "high" || r.PriorityBy != "charlie" || r.PriorityAt == 0 {
			t.Fatalf("after %s: %+v", step, r)
		}
	}
	if _, err := b.DrainCmd(ctx, "coder", "test", DefaultHorizon, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	keeps("the delivery record")
	if err := b.AcceptRequest(ctx, "rq", "coder"); err != nil {
		t.Fatal(err)
	}
	keeps("accept")
	if err := b.BlockRequest(ctx, "rq", "coder", "waiting on master"); err != nil {
		t.Fatal(err)
	}
	keeps("block")
	if ok, err := b.AcquireConductor(ctx, "cond", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireConductor = %v, %v", ok, err)
	}
	if err := b.FencedBoardState(ctx, "cond", "rq", "accepted"); err != nil {
		t.Fatal(err)
	}
	keeps("the conductor's board state")
	if err := b.AcceptRequest(ctx, "rq", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequestAt(ctx, "rq", "coder", "done", "abc1234", "feat/x"); err != nil {
		t.Fatal(err)
	}
	keeps("done")
}

// Availability for real: a trimmed request reads missing and is never next,
// while an accepted one whose body is gone still holds the agent; an expired
// deadline is skipped.
func TestNextRequestOnALiveBoard(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "old", "master", "coder", "", "trimmed soon", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "old", "high", "charlie"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.r.XTrimMaxLen(ctx, StreamKey(b.project, "cmd"), 0).Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "fresh", "master", "coder", "", "kept", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "late", "master", "coder", "", "expires", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "late", "high", "charlie"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a := board["old"].Request.Availability; a != "missing" {
		t.Fatalf("trimmed: %s", a)
	}
	if a := board["late"].Request.Availability; a != "expired" {
		t.Fatalf("past its deadline: %s", a)
	}
	if task, why := NextRequest(board, "coder"); task != "fresh" || why != "" {
		t.Fatalf("next = %q, %q", task, why)
	}
	if err := b.AcceptRequest(ctx, "fresh", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.r.XTrimMaxLen(ctx, StreamKey(b.project, "cmd"), 0).Result(); err != nil {
		t.Fatal(err)
	}
	board, _ = b.Board(ctx)
	if task, why := NextRequest(board, "coder"); task != "fresh" || why != "finish first" {
		t.Fatalf("in hand, body gone: %q, %q", task, why)
	}
}
