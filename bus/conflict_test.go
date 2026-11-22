package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// conflict-owner §4: {p}:conflicts, one field per PR (or branch digest),
// last|count|first|escalated, written by one fenced script; forgotten with
// its FencedCmdOnce marks.

const (
	cfDigA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfDigB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func track(t *testing.T, b *Bus, tr ConflictTrack) ConflictEntry {
	t.Helper()
	e, err := b.FencedConflictTrack(context.Background(), "tok", tr)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestConflictTrackCountsOncePerDigest(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Held: the field is made, first set, nothing counted.
	e := track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigA})
	if e.Count != 0 || e.Last != "" || e.Escalated || e.First < now.Unix()-2 || e.First > now.Unix()+2 {
		t.Fatalf("held: %+v (now %d)", e, now.Unix())
	}
	first := e.First
	e = track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigA, Count: true})
	if e.Count != 1 || e.Last != cfDigA || e.First != first {
		t.Fatalf("counted: %+v", e)
	}
	// The same digest again (a crash before the send, a base move): not counted twice.
	if e = track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigA, Count: true}); e.Count != 1 {
		t.Fatalf("same digest: %+v", e)
	}
	// Held on a new head: nothing changes, so the next pass counts it.
	if e = track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigB}); e.Count != 1 || e.Last != cfDigA {
		t.Fatalf("held on a new head: %+v", e)
	}
	if e = track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigB, Count: true}); e.Count != 2 || e.Last != cfDigB || e.First != first {
		t.Fatalf("a new head: %+v", e)
	}
	rec, err := b.ConflictRecord(ctx)
	if err != nil || rec["pr:7"] != e {
		t.Fatalf("record %+v %v, want %+v", rec, err, e)
	}
}

func TestConflictTrackEscalatedIsNeverCountedAgain(t *testing.T) {
	b := ciLease(t)
	// Escalating a field that does not exist yet (a cap of 0): made, escalated.
	e := track(t, b, ConflictTrack{Field: "br:x", Digest: cfDigA, Escalate: true})
	if !e.Escalated || e.Count != 0 || e.First == 0 {
		t.Fatalf("escalate: %+v", e)
	}
	if e = track(t, b, ConflictTrack{Field: "br:x", Digest: cfDigB, Count: true}); e.Count != 0 || e.Last != "" || !e.Escalated {
		t.Fatalf("counted after the escalation: %+v", e)
	}
}

func TestConflictTrackFenced(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	if _, err := b.FencedConflictTrack(ctx, "other", ConflictTrack{Field: "pr:7", Digest: cfDigA, Count: true}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("not the holder: %v", err)
	}
	if n, _ := b.r.Exists(ctx, ConflictsKey(b.project)).Result(); n != 0 {
		t.Fatal("written without the lease")
	}
	for _, tr := range []ConflictTrack{{Field: "", Digest: cfDigA}, {Field: "pr:7", Digest: "short"}} {
		if _, err := b.FencedConflictTrack(ctx, "tok", tr); err == nil {
			t.Fatalf("%+v accepted", tr)
		}
	}
	b.r.Set(ctx, ConflictsKey(b.project), "x", time.Minute)
	if _, err := b.FencedConflictTrack(ctx, "tok", ConflictTrack{Field: "pr:7", Digest: cfDigA}); err == nil {
		t.Fatal("another type accepted")
	}
}

func TestConflictForgetTakesItsMarks(t *testing.T) {
	b := ciLease(t)
	ctx := context.Background()
	track(t, b, ConflictTrack{Field: "pr:7", Digest: cfDigA, Count: true})
	track(t, b, ConflictTrack{Field: "pr:8", Digest: cfDigA, Count: true})
	for _, f := range []string{"pr:7", "pr:8"} {
		for _, m := range ConflictMarks(f) {
			if _, err := b.FencedCmdOnce(ctx, "tok", m, "ep", "dispatch", "coder", "directive", "x", true); err != nil {
				t.Fatal(err)
			}
		}
	}
	b.r.HSet(ctx, QuotaKey(b.project), "other-mark", "1")
	if err := b.FencedConflictForget(ctx, "other", []string{"pr:7"}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("not the holder: %v", err)
	}
	if err := b.FencedConflictForget(ctx, "tok", []string{"pr:7"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := b.ConflictRecord(ctx)
	if _, ok := rec["pr:7"]; ok || len(rec) != 1 {
		t.Fatalf("record %+v", rec)
	}
	q, _ := b.r.HGetAll(ctx, QuotaKey(b.project)).Result()
	for _, m := range ConflictMarks("pr:7") {
		if _, ok := q[m]; ok {
			t.Fatalf("mark %s kept", m)
		}
	}
	kept := ConflictMarks("pr:8")
	for _, m := range append(kept[:], "other-mark") {
		if _, ok := q[m]; !ok {
			t.Fatalf("mark %s gone", m)
		}
	}
}

// first is the field's birth: a later track never moves it (the age bound
// counts from it).
func TestConflictTrackKeepsFirst(t *testing.T) {
	b := ciLease(t)
	b.r.HSet(context.Background(), ConflictsKey(b.project), "pr:9", cfDigA+"|1|1000|0")
	for _, tr := range []ConflictTrack{{Field: "pr:9", Digest: cfDigB}, {Field: "pr:9", Digest: cfDigB, Count: true}, {Field: "pr:9", Digest: cfDigB, Escalate: true}} {
		if e := track(t, b, tr); e.First != 1000 {
			t.Fatalf("%+v: first %d, want 1000", tr, e.First)
		}
	}
}
