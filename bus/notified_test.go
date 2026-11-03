package bus

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// {p}:notified (notifications spec): every write fenced; a send's value is
// Redis TIME, a baseline's the word; Clear deletes the whole hash.
func TestNotifiedWritesAreFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), NotifiedKey(p)) })
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("lease")
	}
	if err := b.FencedNotifiedSent(ctx, "other", "decision|ask:1|phone"); err != ErrNotHolder {
		t.Fatalf("a lost lease wrote: %v", err)
	}
	if err := b.FencedNotifiedBaseline(ctx, "other", "phone"); err != ErrNotHolder {
		t.Fatalf("a lost lease wrote a baseline: %v", err)
	}
	before := time.Now().Add(-time.Minute).UnixMilli()
	if err := b.FencedNotifiedBaseline(ctx, "tok", "phone", "decision|ask:0|phone"); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedNotifiedSent(ctx, "tok", "decision|ask:1|phone", "fail|disk|1|phone"); err != nil {
		t.Fatal(err)
	}
	got, err := b.Notified(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got["decision|ask:0|phone"] != "baseline" || len(got) != 4 {
		t.Fatalf("record %v", got)
	}
	for _, f := range []string{"_target|phone", "decision|ask:1|phone"} {
		if ms, err := strconv.ParseInt(got[f], 10, 64); err != nil || ms < before {
			t.Fatalf("%s = %q: want Redis TIME in ms", f, got[f])
		}
	}
	if err := b.FencedNotifiedDel(ctx, "other", "fail|disk|1|phone"); err != ErrNotHolder {
		t.Fatalf("a lost lease deleted: %v", err)
	}
	b.FencedNotifiedDel(ctx, "tok", "fail|disk|1|phone")
	if got, _ := b.Notified(ctx); len(got) != 3 {
		t.Fatalf("after delete %v", got)
	}
	if err := b.FencedNotifiedClear(ctx, "other"); err != ErrNotHolder {
		t.Fatalf("a lost lease cleared: %v", err)
	}
	b.FencedNotifiedClear(ctx, "tok")
	if n, _ := b.r.Exists(ctx, NotifiedKey(p)).Result(); n != 0 {
		t.Fatal("not cleared")
	}
	// A notified key alone never makes a deleted project look alive.
	b.FencedNotifiedSent(ctx, "tok", "decision|ask:1|phone")
	b.r.Del(ctx, DispatchKey(p))
	if alive, _ := b.HasKeysBesidesDispatch(ctx); alive {
		t.Fatal("{p}:notified revives a project")
	}
	if c, _ := ClassOf(p, NotifiedKey(p)); c != KeyRuntime {
		t.Fatalf("class %v", c)
	}
}
