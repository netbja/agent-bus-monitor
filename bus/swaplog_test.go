package bus

import (
	"context"
	"errors"
	"testing"
)

// v0.15.1 §11.2: begin goes with the intent, done (or back) with the
// fallback state; a begin left behind by a dead caller reads as interrupted.
func TestSwapLedgerLifecycle(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	in := SwapIntent{ToProfile: "opus", By: "master", OldPID: 42}
	if err := b.BeginSwap(ctx, "foureyes", in, SwapEvent{FromProfile: "review", ToProfile: "opus", By: "master", Reason: "ask 17: swap"}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSwap(ctx, "foureyes", in); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("second begin = %v", err)
	}
	if log, _ := b.SwapLog(ctx, "foureyes", 0); len(log) != 1 || log[0].Event != SwapBegin || log[0].Reason != "ask 17: swap" {
		t.Fatalf("a refused begin was logged, or the begin is wrong: %+v", log)
	}
	fb := &FallbackRecord{OwnProfile: "review", OwnProvider: "openai", Profile: "opus", Provider: "anthropic", By: "master", HeldEpisode: "1792500000"}
	if err := b.FinishSwap(ctx, SwapEvent{Role: "foureyes", ToProfile: "opus", NewPID: 51}, fb); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Fallbacks(ctx)
	if r := got["foureyes"]; r.Profile != "opus" || r.Since == 0 || r.HeldEpisode != "1792500000" {
		t.Fatalf("fallback = %+v", r)
	}
	if err := b.EndSwap(ctx, "foureyes"); err != nil {
		t.Fatal(err)
	}
	if err := b.FinishSwap(ctx, SwapEvent{Role: "foureyes", ToProfile: "review"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Fallbacks(ctx); len(got) != 0 {
		t.Fatalf("the back left a fallback: %+v", got)
	}
	log, _ := b.SwapLog(ctx, "foureyes", 0)
	if len(log) != 3 || log[0].Event != SwapBack || log[1].Event != SwapDone {
		t.Fatalf("ledger = %+v", log)
	}
	// A caller that dies after begin: no live intent, last entry begin.
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if bad, _, _ := b.Interrupted(ctx, "coder"); bad {
		t.Fatal("a live swap reads as interrupted")
	}
	if err := b.EndSwap(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if bad, ev, _ := b.Interrupted(ctx, "coder"); !bad || ev.Event != SwapBegin {
		t.Fatalf("interrupted = %v %+v", bad, ev)
	}
	if err := b.LogSwap(ctx, SwapEvent{Role: "coder", Event: "bogus"}); err == nil {
		t.Fatal("an unknown event was logged")
	}
}
