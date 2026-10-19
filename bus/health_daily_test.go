package bus

import (
	"context"
	"testing"
	"time"
)

func TestFencedHealthDailyWakeRearmsOnRedisDayChange(t *testing.T) {
	b := dialHealth(t)
	ctx := context.Background()
	installHealthProject(t, b, "holder")
	if sent, err := b.FencedHealthDailyWake(ctx, "holder", "master"); err != nil || !sent {
		t.Fatalf("first daily wake = %v, %v", sent, err)
	}
	if err := b.r.Set(ctx, HealthDailyKey(b.project), "previous-day", 48*time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if sent, err := b.FencedHealthDailyWake(ctx, "holder", "master"); err != nil || !sent {
		t.Fatalf("wake after day rollover = %v, %v", sent, err)
	}
}
