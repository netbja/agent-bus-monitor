package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A held Receiver drains what waited, then receives what arrives after, under
// the one token it took: the lease key never changes hands between the two.
func TestReceiverDrainsThenDeliversUnderOneLease(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	before, err := b.Cmd(ctx, "sender", "peer", CmdDirective, "", "while down")
	if err != nil {
		t.Fatal(err)
	}
	r, err := b.TakeReceiver(ctx, "peer", "host-1", DefaultHorizon, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	lease := ReceiverKey(b.project, "peer")
	token, err := b.r.Get(ctx, lease).Result()
	if err != nil || token != r.rc.token {
		t.Fatalf("lease = %q %v, want the receiver's token", token, err)
	}
	var drained []Event
	if n, err := r.Drain(DefaultHorizon, collect(&drained)); err != nil || n != 1 || drained[0].ID != before {
		t.Fatalf("Drain = %d %v %+v, want the entry sent while down", n, err, drained)
	}
	if got, _ := b.r.Get(ctx, lease).Result(); got != token {
		t.Fatalf("lease after the drain = %q, want the same token", got)
	}
	after, err := b.Cmd(ctx, "sender", "peer", CmdDirective, "", "after the drain")
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	if err := r.Deliver("0", func(e Event) (bool, error) { got = append(got, e); return true, nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != after {
		t.Fatalf("Deliver = %+v, want the entry sent after the drain", got)
	}
	if v, _ := b.r.Get(ctx, lease).Result(); v != token {
		t.Fatalf("lease after delivery = %q, want the same token", v)
	}
	r.Close()
	if n, _ := b.r.Exists(ctx, lease).Result(); n != 0 {
		t.Fatal("Close left the lease")
	}
}

// A second receiver for the same agent cannot take the lease while the first
// holds it; a drain under it reads nothing (the second peer under one name).
func TestReceiverIsExclusive(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	r, err := b.TakeReceiver(ctx, "peer", "host-1", DefaultHorizon, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := b.TakeReceiver(ctx, "peer", "host-2", DefaultHorizon, 200*time.Millisecond); !errors.Is(err, ErrReceiverBusy) {
		t.Fatalf("second TakeReceiver = %v, want ErrReceiverBusy", err)
	}
	if _, err := b.TakeReceiver(ctx, "bad name", "host-2", DefaultHorizon, time.Second); err == nil {
		t.Fatal("an invalid agent name was accepted")
	}
}
