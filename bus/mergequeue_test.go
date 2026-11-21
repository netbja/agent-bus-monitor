package bus

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	if err != nil || rec.Reason != "" || rec.Source != "gh" || rec.Unapproved != 2 || len(rec.Conflicting) != 1 || !reflect.DeepEqual(rec.Conflicting[0], MergeConflict{Number: 500, Base: "release"}) || len(rec.Items) != 1 || rec.Items[0].Number != 309 ||
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
	// The CI loop's failure is kept beside a success, and cleared by the next pass (ci-loop §4).
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh", CI: "ci: write failed for PR #4: OOM"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); rec.CI != "ci: write failed for PR #4: OOM" || rec.Reason != "" || rec.ItemsAt == 0 {
		t.Fatalf("a CI failure: %+v", rec)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); rec.CI != "" {
		t.Fatalf("a CI failure not cleared: %+v", rec)
	}
	if ttl := b.r.PTTL(ctx, MergeQueueKey(b.project)).Val(); ttl <= 0 || ttl > MergeQueueTTL {
		t.Fatalf("ttl %v", ttl)
	}
	// conflict-owner §2: what git confirmed, and what it could not judge.
	confirmed := MergeConflict{Number: 7, Base: "dev", Branch: "feat/x", Head: strings.Repeat("a", 40), BaseTip: strings.Repeat("b", 40), Files: []string{"f", "g"}}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh", Conflicting: []MergeConflict{confirmed}, ConflictCheck: "#9: the head is absent here"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); len(rec.Conflicting) != 1 || !reflect.DeepEqual(rec.Conflicting[0], confirmed) || rec.ConflictCheck != "#9: the head is absent here" {
		t.Fatalf("a confirmed conflict: %+v", rec)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "gh"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); rec.ConflictCheck != "" {
		t.Fatalf("the check's reason not cleared: %+v", rec)
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
	if err != nil || len(rec.Conflicting) != 2 || !reflect.DeepEqual(rec.Conflicting, []MergeConflict{{Number: 500}, {Number: 512}}) {
		t.Fatalf("%+v %v", rec.Conflicting, err)
	}
}

// master-idle §3.1: a success writes the undelivered heads; a failure keeps
// the last list, as it keeps the items.
func TestMergeQueueUndelivered(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "mq", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	u := []MergeUndelivered{{Subject: "herdr-6c2-2", Head: "fa2d796f00000000000000000000000000000000", Branch: "feat/herdr-6c2-2", ApprovedAt: 7}}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git", Undelivered: u}); err != nil {
		t.Fatal(err)
	}
	if rec, err := b.MergeQueue(ctx); err != nil || len(rec.Undelivered) != 1 || rec.Undelivered[0] != u[0] {
		t.Fatalf("after a success: %+v %v", rec, err)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git", Reason: "fetch failed"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); len(rec.Undelivered) != 1 {
		t.Fatalf("a failure lost the list: %+v", rec)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git"}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Undelivered == nil || len(rec.Undelivered) != 0 {
		t.Fatalf("an empty success: %+v", rec.Undelivered)
	}
}

// Dropped heads: one field per head, its time Redis's; undrop deletes it;
// the merge-queue pass prunes a drop older than MergeDropKeep in its fenced
// write, a failed pass too.
func TestMergeQueueDropped(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	h := "9b7106db00000000000000000000000000000000"
	if err := b.DropHead(ctx, h, "redundant after operator-notes-fragments", "master"); err != nil {
		t.Fatal(err)
	}
	ds, err := b.DroppedHeads(ctx)
	if d := ds[h]; err != nil || d.Reason != "redundant after operator-notes-fragments" || d.By != "master" || d.At <= 0 {
		t.Fatalf("dropped %+v %v", ds, err)
	}
	if ok, err := b.UndropHead(ctx, h); err != nil || !ok {
		t.Fatalf("undrop %v %v", ok, err)
	}
	if ok, _ := b.UndropHead(ctx, h); ok {
		t.Fatal("undrop of a head not dropped says it was")
	}
	if ds, _ := b.DroppedHeads(ctx); len(ds) != 0 {
		t.Fatalf("after undrop %+v", ds)
	}
	// Prune: an old drop goes, a fresh one stays.
	old := "1111111111111111111111111111111111111111"
	b.r.HSet(ctx, MergeQueueDroppedKey(b.project), old, `{"reason":"old","by":"master","at":1}`)
	if err := b.DropHead(ctx, h, "fresh", "master"); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "mq", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	if err := b.FencedPutMergeQueue(ctx, "mq", MergeQueueRecord{Source: "git", Reason: "a failed pass prunes too"}); err != nil {
		t.Fatal(err)
	}
	ds, _ = b.DroppedHeads(ctx)
	if _, ok := ds[old]; ok || len(ds) != 1 || ds[h].Reason != "fresh" {
		t.Fatalf("after the pass: %+v", ds)
	}
}

// merge-popup-setting (the person's choice A): a delivery marks the queue
// due with the broker's time, the dispatcher reads the mark; a later mark
// differs. The key is runtime and never makes a project look alive.
func TestMergeQueueDueMark(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if v, err := b.MergeQueueDue(ctx); err != nil || v != 0 {
		t.Fatalf("no mark: %d %v", v, err)
	}
	if err := b.MarkMergeQueueDue(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := b.MergeQueueDue(ctx)
	if err != nil || first <= 0 {
		t.Fatalf("mark: %d %v", first, err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := b.MarkMergeQueueDue(ctx); err != nil {
		t.Fatal(err)
	}
	if second, _ := b.MergeQueueDue(ctx); second == first {
		t.Fatalf("a second mark did not change: %d", second)
	}
	if alive, err := b.HasKeysBesidesDispatch(ctx); err != nil || alive {
		t.Fatalf("the mark made the project look alive: %v %v", alive, err)
	}
	if c, _ := ClassOf(b.project, MergeQueueDueKey(b.project)); c != KeyRuntime {
		t.Fatalf("class %v", c)
	}
}
