package bus

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// report-one-row §5 B: {p}:seen, the person's read receipt for report
// notices: first opening wins, Redis TIME, capped at SeenMax, written only
// while the project's cmd stream exists.
func TestSeenFirstOpeningWins(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, SeenKey(p), StreamKey(p, "cmd")) })
	if err := b.MarkSeen(ctx, "bob", "1-0"); err != nil {
		t.Fatal(err)
	}
	if n, _ := b.r.Exists(ctx, SeenKey(p)).Result(); n != 0 {
		t.Fatal("a project with no cmd stream got a seen key")
	}
	id, err := b.Cmd(ctx, "master", "bob", CmdDirective, "", "x")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Minute)
	if err := b.MarkSeen(ctx, "bob", id); err != nil {
		t.Fatal(err)
	}
	first, err := b.Seen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := first[id]
	if !ok || s.By != "bob" || s.At.Before(before) || s.At.After(time.Now().Add(time.Minute)) {
		t.Fatalf("seen %+v", first)
	}
	time.Sleep(5 * time.Millisecond)
	if err := b.MarkSeen(ctx, "carol", id); err != nil {
		t.Fatal(err)
	}
	again, _ := b.Seen(ctx)
	if again[id] != s {
		t.Fatalf("a later opening replaced the first: %+v vs %+v", again[id], s)
	}
	if err := b.MarkSeen(ctx, "bob", "not-an-id"); err == nil {
		t.Fatal("a field that is no stream id was written")
	}
	if err := b.MarkSeen(ctx, "b|ob", id); err == nil {
		t.Fatal("a person with a separator was written")
	}
}

func TestSeenIsCapped(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, SeenKey(p), StreamKey(p, "cmd")) })
	if _, err := b.Cmd(ctx, "master", "bob", CmdDirective, "", "x"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 1; i <= SeenMax+10; i++ {
		ids = append(ids, fmt.Sprintf("%d-%d", 1000+i, i%3))
	}
	if err := b.MarkSeen(ctx, "bob", ids...); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Seen(ctx)
	if len(got) != SeenMax {
		t.Fatalf("%d fields, want %d", len(got), SeenMax)
	}
	for _, old := range ids[:10] {
		if _, ok := got[old]; ok {
			t.Fatalf("the oldest id %s was kept", old)
		}
	}
	if _, ok := got[ids[len(ids)-1]]; !ok {
		t.Fatal("the newest id was pruned")
	}
}

func TestSeenKeyIsRuntime(t *testing.T) {
	if c, _ := ClassOf("demo", SeenKey("demo")); c != KeyRuntime {
		t.Fatalf("seen is %v", c)
	}
}
