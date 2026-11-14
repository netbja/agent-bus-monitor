package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// popReady registers a pop with a group whose handled directive is drained,
// the dispatch lease held as "tok"; nothing else waits for it.
func popReady(t *testing.T, b *Bus, role string) {
	t.Helper()
	ctx := context.Background()
	p := b.Project()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	if _, err := b.Status(ctx, role, "idle", "done", AgentIdent{Pane: "p-" + role, PID: 42, PIDStart: 4200}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cmd(ctx, "master", role, "directive", "", "handled already"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DrainCmd(ctx, role, role, DefaultHorizon, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.r.Del(ctx, SwapKey(p, role), DispatchKey(p))
		b.r.XGroupDestroy(ctx, StreamKey(p, "cmd"), role)
		b.r.HDel(ctx, AgentsKey(p), role)
	})
}

// pop-retire-auto: the dispatcher's own retire of a done pop takes the
// retire intent fenced by its lease, with its nonce; every step is refused
// without the lease, and only its own intent is ended.
func TestAutoRetireIntentIsFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	popReady(t, b, "security")
	ev := SwapEvent{By: "dispatch", OldPID: 42, OldPane: "p-security", Reason: "done"}
	if _, err := b.BeginAutoRetire(ctx, "other", "security", "n1", 4200, ev); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("a begin without the lease: %v", err)
	}
	if _, err := b.BeginAutoRetire(ctx, "tok", "nobody", "n1", 4200, ev); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("a begin for an unregistered role: %v", err)
	}
	own, err := b.BeginAutoRetire(ctx, "tok", "security", "n1", 4200, ev)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.SwapIntentRaw(ctx, "security"); raw != own {
		t.Fatalf("stored %q, returned %q: ForgetIfClear compares the bytes", raw, own)
	}
	in, held, _ := b.SwapIntentOf(ctx, "security")
	if !held || in.Kind != SwapKindRetire || in.Nonce != "n1" || in.By != "dispatch" || in.OldPID != 42 || in.OldStart != 4200 {
		t.Fatalf("intent %+v", in)
	}
	if _, err := b.BeginAutoRetire(ctx, "tok", "security", "n2", 4200, ev); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("a second begin: %v", err)
	}
	log, _ := b.SwapLog(ctx, "security", 5)
	if len(log) != 1 || log[0].Event != SwapBegin || log[0].Kind != SwapKindRetire || log[0].By != "dispatch" {
		t.Fatalf("ledger %+v", log)
	}
	if ok, _ := b.AutoRetireHeld(ctx, "tok", "security", "n1"); !ok {
		t.Fatal("its own intent not held")
	}
	if ok, _ := b.AutoRetireHeld(ctx, "tok", "security", "n2"); ok {
		t.Fatal("another nonce held")
	}
	if ok, _ := b.AutoRetireHeld(ctx, "other", "security", "n1"); ok {
		t.Fatal("held without the lease")
	}
	if err := b.FencedLogSwap(ctx, "other", SwapEvent{Role: "security", Event: SwapFailed, By: "dispatch", Error: "x"}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("a ledger entry without the lease: %v", err)
	}
	if err := b.FencedRetireSwap(ctx, "other", SwapEvent{Role: "security", By: "dispatch"}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("a retire entry without the lease: %v", err)
	}
	if err := b.EndAutoRetire(ctx, "tok", "security", "n2"); !errors.Is(err, ErrNotOwnIntent) {
		t.Fatalf("another nonce's end: %v", err)
	}
	if err := b.EndAutoRetire(ctx, "other", "security", "n1"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("an end without the lease: %v", err)
	}
	if err := b.EndAutoRetire(ctx, "tok", "security", "n1"); err != nil {
		t.Fatal(err)
	}
	if _, held, _ := b.SwapIntentOf(ctx, "security"); held {
		t.Fatal("the intent outlived its end")
	}
	// A self-repair's intent is not a retire's.
	if err := b.BeginSelfRepair(ctx, "tok", "security", "n1", SwapEvent{By: "dispatch"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.AutoRetireHeld(ctx, "tok", "security", "n1"); ok {
		t.Fatal("a self-repair's intent read as a retire's")
	}
}

// The forget, fenced: refused without the lease; with it, the pop is
// unregistered, its group kept, the retire entry written with its fallback
// record cleared; the CLI's unfenced ForgetIfClear is unchanged.
func TestAutoRetireForgetIsFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	popReady(t, b, "security")
	own, err := b.BeginAutoRetire(ctx, "tok", "security", "n1", 4200, SwapEvent{By: "dispatch", OldPID: 42})
	if err != nil {
		t.Fatal(err)
	}
	was := retiredAs(t, b, "security")
	if _, err := b.ForgetIfClearFenced(ctx, "other", "security", own, was); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("a forget without the lease: %v", err)
	}
	if !registered(t, b, "security") {
		t.Fatal("unregistered without the lease")
	}
	res, err := b.ForgetIfClearFenced(ctx, "tok", "security", own, was)
	if err != nil || !res.Forgot {
		t.Fatalf("forget %+v %v", res, err)
	}
	if registered(t, b, "security") {
		t.Fatal("still registered")
	}
	if g, _ := b.r.XInfoGroups(ctx, StreamKey(b.Project(), "cmd")).Result(); !hasGroup(g, "security") {
		t.Fatal("the consumer group was not kept")
	}
	if err := b.FencedRetireSwap(ctx, "tok", SwapEvent{Role: "security", By: "dispatch", Forgot: "true", Kind: SwapKindRetire}); err != nil {
		t.Fatal(err)
	}
	log, _ := b.SwapLog(ctx, "security", 5)
	if log[0].Event != SwapRetire || log[0].Forgot != "true" {
		t.Fatalf("ledger %+v", log[0])
	}
}

func hasGroup(groups []redis.XInfoGroup, name string) bool {
	for _, g := range groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// The last ledger entry decides; the self-repair shadow's records are
// looked past; a kept retire (forgot=false) or a later begin is no retire.
func TestRetiredForgotten(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, SwapsKey(b.Project())) })
	if ok, _ := b.RetiredForgotten(ctx, "pop"); ok {
		t.Fatal("no ledger read as retired")
	}
	b.RetireSwap(ctx, SwapEvent{Role: "pop", By: "master", Forgot: "false"})
	if ok, _ := b.RetiredForgotten(ctx, "pop"); ok {
		t.Fatal("a kept retire read as forgotten")
	}
	b.RetireSwap(ctx, SwapEvent{Role: "pop", By: "master", Forgot: "true"})
	b.LogSwap(ctx, SwapEvent{Role: "pop", Event: SwapWouldRelaunch, Kind: SwapKindSelfRepair, By: "dispatch", Episode: "1:1"})
	b.LogSwap(ctx, SwapEvent{Role: "other", Event: SwapBegin, By: "master"})
	if ok, err := b.RetiredForgotten(ctx, "pop"); !ok || err != nil {
		t.Fatalf("retired and forgotten: %v %v", ok, err)
	}
	b.LogSwap(ctx, SwapEvent{Role: "pop", Event: SwapBegin, By: "master"})
	if ok, _ := b.RetiredForgotten(ctx, "pop"); ok {
		t.Fatal("a later begin read as retired")
	}
}
