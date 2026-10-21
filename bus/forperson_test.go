package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A notice the dispatcher writes for the person carries for=person; it stays
// fenced like every dispatcher write (busmon UX item 8, kind 4).
func TestFencedNotifyForPerson(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if _, err := b.FencedNotifyForPerson(ctx, "tok", "dispatch", "x"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if _, err := b.FencedNotifyForPerson(ctx, "tok", "dispatch", "coder: nobody can act"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedNotify(ctx, "tok", "dispatch", "trim gap"); err != nil {
		t.Fatal(err)
	}
	if sent, err := b.FencedNotifyOnceForPerson(ctx, "tok", "longhold:anthropic", "100", "dispatch", "long pause"); err != nil || !sent {
		t.Fatalf("once = %v, %v", sent, err)
	}
	if sent, _ := b.FencedNotifyOnceForPerson(ctx, "tok", "longhold:anthropic", "100", "dispatch", "long pause"); sent {
		t.Fatal("once sent twice in one episode")
	}
	evs, _, err := b.Recent(ctx, []string{"notify"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.Message+"="+e.For)
	}
	want := []string{"coder: nobody can act=person", "trim gap=", "long pause=person"}
	if len(got) != len(want) {
		t.Fatalf("notify entries %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notify entries %q, want %q", got, want)
		}
	}
}

func TestParseEntryReadsFor(t *testing.T) {
	e := ParseEntry("demo:notify", "1-0", map[string]string{"from": "dispatch", "message": "m", "for": ForPerson})
	if e.For != ForPerson {
		t.Fatalf("For = %q", e.For)
	}
	if e := ParseEntry("demo:notify", "1-0", map[string]string{"from": "dispatch", "message": "m"}); e.For != "" {
		t.Fatalf("an old notify reads For %q", e.For)
	}
}
