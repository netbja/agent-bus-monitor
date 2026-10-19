package bus

import (
	"context"
	"testing"
	"time"
)

func TestFencedSetUsageIsFencedAndStamped(t *testing.T) {
	b := dialTest(t)
	t.Cleanup(func() { _, _ = DeleteProject(context.Background(), b.r, b.project) })
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedSetUsage(ctx, "holder", "coder", UsageSnapshot{Ctx: "12k", TS: 1}); err != nil {
		t.Fatalf("FencedSetUsage: %v", err)
	}
	rows, err := b.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows["coder"].TS < 1_000_000_000_000 {
		t.Fatalf("timestamp was not replaced with Redis TIME: %d", rows["coder"].TS)
	}
	if err := b.FencedSetUsage(ctx, "stale", "coder", UsageSnapshot{Ctx: "13k"}); err != ErrNotHolder {
		t.Fatalf("stale writer = %v", err)
	}
}
