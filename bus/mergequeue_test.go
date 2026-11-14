package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMergeQueueRecord(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if rec, err := b.MergeQueue(ctx); err != nil || rec.ItemsAt != 0 || rec.AttemptedAt != 0 {
		t.Fatalf("no record: %+v %v", rec, err)
	}
	if ok, err := b.AcquireDispatch(ctx, "mq", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	items := []MergeItem{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: "f98091be429b220f63dc8b4b2dde3ef7d6d832a5",
		Subjects: []string{"request-priority"}, Approvals: []MergeApproval{{Reviewer: "foureyes", At: 1, Message: "suites pass"}}, ApprovedAt: 1}}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh", Items: items, Unapproved: 2, Conflicting: []MergeConflict{{Number: 500, Base: "release"}}}); err != nil {
		t.Fatal(err)
	}
	rec, err := b.MergeQueue(ctx)
	if err != nil || rec.Reason != "" || rec.Source != "gh" || rec.Unapproved != 2 || len(rec.Conflicting) != 1 || rec.Conflicting[0] != (MergeConflict{Number: 500, Base: "release"}) || len(rec.Items) != 1 || rec.Items[0].Number != 309 ||
		rec.ItemsAt == 0 || rec.AttemptedAt != rec.ItemsAt {
		t.Fatalf("after a success: %+v %v", rec, err)
	}
	first := rec.ItemsAt
	// Two failed passes: the attempt and the reason move; the items and their time stay (reviewer-codex).
	for i := 0; i < 2; i++ {
		time.Sleep(3 * time.Millisecond)
		if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh", Reason: "gh timed out"}); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ = b.MergeQueue(ctx)
	if rec.Reason != "gh timed out" || rec.ItemsAt != first || rec.AttemptedAt <= first || len(rec.Items) != 1 || rec.Unapproved != 2 {
		t.Fatalf("after failures: %+v", rec)
	}
	// A success with nothing waiting is an empty list, not a missing one.
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); rec.Reason != "" || len(rec.Items) != 0 || rec.ItemsAt <= first || rec.Source != "git" {
		t.Fatalf("an empty success: %+v", rec)
	}
	if ttl := b.r.PTTL(ctx, MergeQueueKey(b.project)).Val(); ttl <= 0 || ttl > MergeQueueTTL {
		t.Fatalf("ttl %v", ttl)
	}
	if err := b.FencedPutMergeQueue(ctx, "not-holder", MergeQueueRecord{Source: "gh"}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
}

// The cache alone never makes a project look alive, and it is runtime.
func TestMergeQueueKeyIsRuntime(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "mq", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git"}); err != nil {
		t.Fatal(err)
	}
	if alive, err := b.HasKeysBesidesDispatch(ctx); err != nil || alive {
		t.Fatalf("the cache made the project look alive: %v %v", alive, err)
	}
	if c, _ := ClassOf(b.project, MergeQueueKey(b.project)); c != KeyRuntime {
		t.Fatalf("class %v", c)
	}
}

// The first form of the field held bare PR numbers ([500]): read as conflicts
// with no base named, never a corrupt record (reviewer-codex), so a cache
// written by it still reads until the next success replaces it.
func TestMergeQueueReadsBareConflictNumbers(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.r.HSet(ctx, MergeQueueKey(b.Project()), "items_at", "1", "items", "[]", "conflicting", "[500, 512]").Err(); err != nil {
		t.Fatal(err)
	}
	rec, err := b.MergeQueue(ctx)
	if err != nil || len(rec.Conflicting) != 2 || rec.Conflicting[0] != (MergeConflict{Number: 500}) || rec.Conflicting[1] != (MergeConflict{Number: 512}) {
		t.Fatalf("%+v %v", rec.Conflicting, err)
	}
}
