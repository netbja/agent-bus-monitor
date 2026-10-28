package bus

import (
	"context"
	"errors"
	"testing"
)

// A reload (lifecycle spec, part C) reuses the swap intent with kind=reload:
// the dispatcher's swap rules apply unchanged, while busmon, the ledger and
// the interrupted check can tell it apart. Its intent is refused for a role
// with no snapshot (a deleted project), allowed in tab mode (no services
// marker), and it never touches {p}:fallback.
func TestReloadIntentAndLedgerRedis(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	reload := SwapIntent{Kind: SwapKindReload, By: "sentinel", OldPID: 42, OldStart: 7}
	ev := SwapEvent{Kind: SwapKindReload, By: "sentinel", Reason: "client update", OldPID: 42, OldPane: "w1:p2"}

	if err := b.BeginSwap(ctx, "coder", reload, ev); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("reload of a role with no snapshot (a deleted project): %v", err)
	}
	if live, _ := b.Swapping(ctx, "coder"); live {
		t.Fatal("a refused reload left an intent")
	}
	if log, _ := b.SwapLog(ctx, "coder", 0); len(log) != 0 {
		t.Fatalf("a refused reload was logged: %+v", log)
	}
	// Tab mode (up --tabs): no services marker, only the agent's snapshot.
	if _, err := b.Status(ctx, "coder", "idle", "", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.ServicesInstalled(ctx); ok {
		t.Fatal("the fixture must have no services marker")
	}

	// A role on a fallback keeps its record through another role's reload.
	fb := &FallbackRecord{OwnProfile: "review", OwnProvider: "openai", Profile: "opus", Provider: "anthropic"}
	if err := b.FinishSwap(ctx, SwapEvent{Role: "foureyes", ToProfile: "opus"}, fb); err != nil {
		t.Fatal(err)
	}

	if err := b.BeginSwap(ctx, "coder", reload, ev); err != nil {
		t.Fatalf("BeginSwap(reload): %v", err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("a swap during a reload: %v", err)
	}
	intents, err := b.SwapIntents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if in := intents["coder"]; in.Kind != SwapKindReload || in.By != "sentinel" || in.OldPID != 42 {
		t.Fatalf("intent %+v", in)
	}
	if bad, _, _ := b.Interrupted(ctx, "coder"); bad {
		t.Fatal("a live reload reads as interrupted")
	}
	if err := b.FinishReload(ctx, SwapEvent{Role: "coder", By: "sentinel", NewPID: 51, NewPane: "w1:p3"}); err != nil {
		t.Fatalf("FinishReload: %v", err)
	}
	if err := b.EndSwap(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	log, _ := b.SwapLog(ctx, "coder", 0)
	if len(log) != 2 || log[0].Event != SwapReloaded || log[0].Kind != SwapKindReload || log[0].NewPID != 51 ||
		log[1].Event != SwapBegin || log[1].Kind != SwapKindReload || log[1].Reason != "client update" {
		t.Fatalf("ledger %+v", log)
	}
	if got, _ := b.Fallbacks(ctx); got["foureyes"].Profile != "opus" || len(got) != 1 {
		t.Fatalf("a reload touched the fallback state: %+v", got)
	}

	// A reload whose caller died: the begin reads as interrupted, with its kind.
	if err := b.BeginSwap(ctx, "coder", reload, ev); err != nil {
		t.Fatal(err)
	}
	if err := b.EndSwap(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if bad, last, _ := b.Interrupted(ctx, "coder"); !bad || last.Kind != SwapKindReload {
		t.Fatalf("interrupted reload: %v %+v", bad, last)
	}

	// A plain swap's intent and entries carry no kind, as before.
	if err := b.BeginSwap(ctx, "kimi", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if in, _ := b.SwapIntents(ctx); in["kimi"].Kind != "" {
		t.Fatalf("swap intent kind %q", in["kimi"].Kind)
	}
}
