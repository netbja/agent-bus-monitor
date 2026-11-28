package bus

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"
)

// The pass's finding, fenced and stamped with the broker's time: a token that
// does not hold the lease writes nothing; a write replaces the whole hash.
func TestFencedPutLosable(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.FencedPutLosable(ctx, "tok", map[string]Losable{"coder": {Branches: []string{"coder/x"}}}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("no lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	before, _ := b.ServerTime(ctx)
	if err := b.FencedPutLosable(ctx, "tok", map[string]Losable{"coder": {Branches: []string{"coder/x"}}, "docs": {}, "Bad Name": {}}); err != nil {
		t.Fatal(err)
	}
	rec, err := b.LosableRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 2 || !slices.Equal(rec["coder"].Branches, []string{"coder/x"}) || rec["docs"].Branches == nil || rec["docs"].Stashes == nil {
		t.Fatalf("record: %+v", rec)
	}
	if at := time.UnixMilli(rec["coder"].At); at.Before(before.Add(-time.Second)) || at.After(before.Add(time.Minute)) {
		t.Fatalf("at %v, broker %v", at, before)
	}
	if ttl := b.r.PTTL(ctx, LosableKey(b.project)).Val(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl %v", ttl)
	}
	if err := b.FencedPutLosable(ctx, "tok", map[string]Losable{"docs": {Stashes: []string{"On dev: docs wip"}}}); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.LosableRecord(ctx); len(rec) != 1 || !slices.Equal(rec["docs"].Stashes, []string{"On dev: docs wip"}) {
		t.Fatalf("replaced: %+v", rec)
	}
}

// Fresh only for LosableFresh after its write, and never from the future.
func TestFreshLosable(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	rec := map[string]Losable{
		"fresh":  {At: now.Add(-LosableFresh + time.Second).UnixMilli()},
		"stale":  {At: now.Add(-LosableFresh).UnixMilli()},
		"future": {At: now.Add(time.Second).UnixMilli()},
	}
	for role, want := range map[string]bool{"fresh": true, "stale": false, "future": false, "absent": false} {
		if _, ok := FreshLosable(rec, role, now); ok != want {
			t.Errorf("%s: %v, want %v", role, ok, want)
		}
	}
}

// ReadOwes takes a fresh finding as item 5: an unpushed branch and a stash
// owe, an empty finding owes nothing (stopped, not lost); a role the pass did
// not judge, or a stale finding, stays unknown.
func TestReadOwesLosable(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := b.FencedPutLosable(ctx, "tok", map[string]Losable{
		"coder": {Branches: []string{"coder/x"}}, "docs": {}, "sec": {Stashes: []string{"On sec/y: sec wip"}}, "old": {},
	}); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-LosableFresh - time.Minute).UnixMilli()
	b.r.HSet(ctx, LosableKey(b.project), "old", `{"branches":[],"stashes":[],"at":`+strconv.FormatInt(stale, 10)+`}`)
	owes := b.ReadOwes(ctx, map[string]AgentSnapshot{"coder": {}, "docs": {}, "sec": {}, "old": {}, "architect": {}})
	if c := owes["coder"]; !slices.Equal(c.Unpushed, []string{"coder/x"}) || slices.Contains(c.Unknown, UnknownLosable) {
		t.Fatalf("coder: %+v", c)
	}
	if s, r := Presence(PresenceIn{Pane: "k/t1", TermsRead: true, Owes: owes["coder"]}); s != PresenceLost || r != "unpushed coder/x" {
		t.Fatalf("coder: %s %q", s, r)
	}
	if o := owes["sec"]; !o.Stash || slices.Contains(o.Unknown, UnknownLosable) {
		t.Fatalf("sec: %+v", o)
	}
	d := owes["docs"]
	if d.Something() || len(d.Unknown) != 0 {
		t.Fatalf("docs: %+v", d)
	}
	if s, _ := Presence(PresenceIn{Pane: "k/t1", TermsRead: true, Owes: d}); s != PresenceStopped {
		t.Fatalf("docs owing nothing: %s", s)
	}
	for _, role := range []string{"old", "architect"} {
		if o := owes[role]; !slices.Equal(o.Unknown, []string{UnknownLosable}) {
			t.Errorf("%s: %+v", role, o)
		}
	}
}

// An unreadable record leaves item 5 unknown for everyone.
func TestReadOwesLosableUnreadable(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.r.Set(ctx, LosableKey(b.project), "not a hash", 0)
	if o := b.ReadOwes(ctx, map[string]AgentSnapshot{"docs": {}})["docs"]; !slices.Equal(o.Unknown, []string{UnknownLosable}) {
		t.Fatalf("%+v", o)
	}
}
