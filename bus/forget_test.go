package bus

import (
	"context"
	"testing"
	"time"
)

func TestForgetAgentRemovesItsKeysAndGroupOnly(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	for _, a := range []string{"old", "kept"} {
		if _, err := b.Status(ctx, a, "working", "busy", AgentIdent{}); err != nil {
			t.Fatal(err)
		}
		if err := b.SetUsage(ctx, a, UsageSnapshot{Model: "m"}); err != nil {
			t.Fatal(err)
		}
		if err := b.Arm(ctx, a, "c1", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	b.r.HSet(ctx, WakeKey(p, "old"), "state", "prompted")
	b.r.HSet(ctx, GateKey(p, "old"), "ref1", "x")
	t.Cleanup(func() {
		b.r.Del(ctx, WakeKey(p, "old"), GateKey(p, "old"), ArmedKey(p, "old"), ArmedKey(p, "kept"))
	})
	found, err := b.ForgetAgent(ctx, "old")
	if err != nil || !found {
		t.Fatalf("ForgetAgent = %v, %v", found, err)
	}
	snaps, _ := b.Agents(ctx)
	if _, ok := snaps["old"]; ok {
		t.Fatal("snapshot kept")
	}
	if _, ok := snaps["kept"]; !ok {
		t.Fatal("another agent's snapshot went with it")
	}
	if n, _ := b.r.Exists(ctx, WakeKey(p, "old"), GateKey(p, "old"), ArmedKey(p, "old")).Result(); n != 0 {
		t.Fatalf("%d per-agent keys left", n)
	}
	groups, _ := b.GroupNames(ctx)
	for _, g := range groups {
		if g == "old" {
			t.Fatal("cmd group kept")
		}
	}
	if n, _ := b.r.Exists(ctx, ArmedKey(p, "kept")).Result(); n != 1 {
		t.Fatal("the other agent lost its armed key")
	}
	if found, err := b.ForgetAgent(ctx, "old"); err != nil || found {
		t.Fatalf("second forget = %v, %v; want nothing found", found, err)
	}
	if _, err := b.ForgetAgent(ctx, "Bad Name"); err == nil {
		t.Fatal("invalid name accepted")
	}
}
