package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Self-repair's intent (self-repair spec 4.1, step 2): the role's swap
// intent with kind self-repair and a nonce, taken and ended only by the
// dispatch lease holder, ended only by its own nonce, so one relauncher at a
// time and none next to a reload, a swap or a retire.
func TestSelfRepairIntentRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	ev := SwapEvent{By: "dispatch", Reason: "process gone 70s", OldPID: 241, OldPane: "w1:p2"}

	if _, err := b.Status(ctx, "foureyes", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n1", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("no lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.BeginSelfRepair(ctx, "other", "foureyes", "n1", ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("another token: %v", err)
	}
	if live, _ := b.Swapping(ctx, "foureyes"); live {
		t.Fatal("a refused self-repair left an intent")
	}
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "", ev); err == nil {
		t.Fatal("an intent without a nonce")
	}
	if err := b.BeginSelfRepair(ctx, "tok", "coder", "n1", ev); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("a role with no snapshot: %v", err)
	}

	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n1", ev); err != nil {
		t.Fatalf("BeginSelfRepair: %v", err)
	}
	in, held, err := b.SwapIntentOf(ctx, "foureyes")
	if err != nil || !held || in.Kind != SwapKindSelfRepair || in.Nonce != "n1" || in.By != "dispatch" || in.OldPID != 241 {
		t.Fatalf("intent %+v held %v err %v", in, held, err)
	}
	log, _ := b.SwapLog(ctx, "foureyes", 0)
	if len(log) != 1 || log[0].Event != SwapBegin || log[0].Kind != SwapKindSelfRepair || log[0].Reason != "process gone 70s" {
		t.Fatalf("ledger %+v", log)
	}
	// One relauncher at a time: a second self-repair, a reload and a swap are refused.
	if err := b.BeginSelfRepair(ctx, "tok", "foureyes", "n2", ev); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("second self-repair: %v", err)
	}
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{Kind: SwapKindReload, By: "sentinel"}); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("a reload next to it: %v", err)
	}

	// Right before the split: still its own nonce and still the holder.
	for _, c := range []struct {
		token, nonce string
		want         bool
	}{{"tok", "n1", true}, {"tok", "n2", false}, {"other", "n1", false}} {
		if got, err := b.SelfRepairHeld(ctx, c.token, "foureyes", c.nonce); err != nil || got != c.want {
			t.Errorf("held(%s, %s) = %v, %v; want %v", c.token, c.nonce, got, err, c.want)
		}
	}

	// The end: only the holder, only its own nonce.
	if err := b.EndSelfRepair(ctx, "other", "foureyes", "n1"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("end by another token: %v", err)
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n2"); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("end by another nonce: %v", err)
	}
	if live, _ := b.Swapping(ctx, "foureyes"); !live {
		t.Fatal("a refused end removed the intent")
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); err != nil {
		t.Fatalf("EndSelfRepair: %v", err)
	}
	if _, held, _ := b.SwapIntentOf(ctx, "foureyes"); held {
		t.Fatal("the intent outlived its end")
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); err != nil {
		t.Fatalf("an end already done: %v", err)
	}
	if got, _ := b.SelfRepairHeld(ctx, "tok", "foureyes", "n1"); got {
		t.Fatal("held after its end")
	}

	// Another kind's intent is never ended by a self-repair, even with its nonce.
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{Kind: SwapKindRetire, Nonce: "n1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSelfRepair(ctx, "tok", "foureyes", "n1"); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("a retire's intent ended: %v", err)
	}
	if ttl := b.r.PTTL(ctx, SwapKey(b.project, "foureyes")).Val(); ttl <= 0 || ttl > SwapTTL {
		t.Fatalf("intent TTL %v", ttl)
	}
}

func TestSwapKindWord(t *testing.T) {
	for kind, want := range map[string]string{"": "swap", SwapKindReload: "reload", SwapKindRetire: "retire", SwapKindSelfRepair: "self-repair", "odd": "swap"} {
		if got := SwapKindWord(kind); got != want {
			t.Errorf("%q: %q, want %q", kind, got, want)
		}
	}
}
