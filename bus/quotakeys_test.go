package bus

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestQuotaWritesAreFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), QuotaKey(p), PauseKey(p)) })
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("lease")
	}
	snap := BudgetSnapshot{Provider: "openai", Windows: []BudgetWindow{{Name: "5h", UsedPct: 40, ResetsAt: 1792116530, Minutes: 300}}, ObservedAt: 1}
	if err := b.FencedPutBudgets(ctx, "other", map[string]BudgetSnapshot{"openai": snap}); err != ErrNotHolder {
		t.Fatalf("budget with a lost lease: %v", err)
	}
	if err := b.FencedPutBudgets(ctx, "tok", map[string]BudgetSnapshot{"openai": snap}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Budgets(ctx)
	if len(got["openai"].Windows) != 1 || got["openai"].Windows[0].UsedPct != 40 {
		t.Fatalf("budget %+v", got)
	}
	if err := b.FencedQuotaSet(ctx, "other", "hold:openai", "1"); err != ErrNotHolder {
		t.Fatalf("quota mark with a lost lease: %v", err)
	}
	b.FencedQuotaSet(ctx, "tok", "hold:openai", "1792116530", "warn:openai:5h", "1792116530")
	b.FencedQuotaDel(ctx, "tok", "hold:openai")
	if m, _ := b.QuotaMarks(ctx); m["hold:openai"] != "" || m["warn:openai:5h"] != "1792116530" {
		t.Fatalf("marks %v", m)
	}
}

func TestQuotaPauseNeverReplacesAnotherPause(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), PauseKey(p), StreamKey(p, "cmd")) })
	b.AcquireDispatch(ctx, "tok", time.Minute)
	until := time.Now().Add(time.Hour).UnixMilli()
	if ok, err := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", until); err != nil || !ok {
		t.Fatalf("first quota pause: %v %v", ok, err)
	}
	pz, _ := b.Pause(ctx)
	if pz == nil || pz.Reason != "quota anthropic" || pz.Until != until || pz.Notify != "master" || pz.By != "dispatch" {
		t.Fatalf("pause %+v", pz)
	}
	if ok, _ := b.FencedQuotaPause(ctx, "tok", "openai", "master", until); ok {
		t.Fatal("another provider replaced the quota pause")
	}
	if ok, _ := b.FencedQuotaUnpause(ctx, "tok", "openai", "dispatch"); ok {
		t.Fatal("unpaused another provider's pause")
	}
	if ok, _ := b.FencedQuotaUnpause(ctx, "tok", "anthropic", "dispatch"); !ok {
		t.Fatal("own quota pause not removed")
	}
	// The early end tells the notify target, like a pause ending on time.
	msgs, _ := b.r.XRevRangeN(ctx, StreamKey(p, "cmd"), "+", "-", 1).Result()
	if len(msgs) != 1 || msgs[0].Values["target"] != "master" || msgs[0].Values["from"] != "dispatch" || !strings.Contains(fmt.Sprint(msgs[0].Values["command"]), "Project resumed") {
		t.Fatalf("no resume directive after an early quota unpause: %+v", msgs)
	}
	// A person's pause carrying the same reason is never taken for the
	// dispatcher's: only the exact record the dispatcher wrote is ended.
	if _, err := b.SetPause(ctx, "charlie", "master", "quota anthropic", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.FencedQuotaUnpause(ctx, "tok", "anthropic", "dispatch"); ok {
		t.Fatal("ended a person's pause that only shares the reason")
	}
	if pz, _ := b.Pause(ctx); pz == nil || pz.By != "charlie" {
		t.Fatalf("the person's pause is gone: %+v", pz)
	}
	if _, err := b.SetPause(ctx, "charlie", "master", "dinner", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", until); ok {
		t.Fatal("a quota pause replaced a person's pause")
	}
	if _, err := b.FencedQuotaPause(ctx, "lost", "anthropic", "master", until); err != ErrNotHolder {
		t.Fatalf("lost lease: %v", err)
	}
}

// F5: the budget hash is replaced, not merged; the lease is checked even for
// an empty set.
func TestFencedPutBudgetsReplaces(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p)) })
	b.AcquireDispatch(ctx, "tok", time.Minute)
	b.SetBudget(ctx, "legacy", BudgetSnapshot{Provider: "legacy", SessionPct: 50})
	if err := b.FencedPutBudgets(ctx, "tok", map[string]BudgetSnapshot{"openai": {Provider: "openai"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Budgets(ctx)
	if _, ok := got["legacy"]; ok || len(got) != 1 {
		t.Fatalf("budgets %v: the legacy row lingered", got)
	}
	if err := b.FencedPutBudgets(ctx, "other", nil); err != ErrNotHolder {
		t.Fatalf("empty set with a lost lease: %v", err)
	}
}

func TestProviderForFallsBackOnTheClient(t *testing.T) {
	if ProviderFor("grok", "") != "xai" || ProviderFor("codex", "custom") != "custom" || ProviderFor("mystery", "") != "" {
		t.Fatal("ProviderFor")
	}
}
