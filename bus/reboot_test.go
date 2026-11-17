package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

// reboot-resume §3: {p}:reboot is written and deleted under the lease only,
// its at from Redis TIME; each verdict carries its own fields.
func TestRebootRecordFenced(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	if _, ok, err := b.Reboot(ctx); err != nil || ok {
		t.Fatalf("empty: %v %v", ok, err)
	}
	if err := b.FencedPutReboot(ctx, "other", RebootRecord{Boot: "B", Verdict: RebootIntentional}); err != ErrNotHolder {
		t.Fatalf("another token wrote: %v", err)
	}
	r := RebootRecord{Boot: "B", Verdict: RebootRestarting, N: 2, Max: 3, Window: (24 * time.Hour).Milliseconds()}
	if err := b.FencedPutReboot(ctx, "tok", r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := b.Reboot(ctx)
	if err != nil || !ok || got.Boot != "B" || got.Verdict != RebootRestarting || got.N != 2 || got.Max != 3 || got.Window != r.Window || got.At <= 0 {
		t.Fatalf("read %+v %v %v", got, ok, err)
	}
	raw, _ := b.r.Get(ctx, RebootKey(b.project)).Result()
	for _, absent := range []string{`"why"`} {
		if strings.Contains(raw, absent) {
			t.Fatalf("restarting carries %s: %s", absent, raw)
		}
	}
	// The intentional verdict carries no why and no count.
	if err := b.FencedPutReboot(ctx, "tok", RebootRecord{Boot: "B", Verdict: RebootIntentional}); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.r.Get(ctx, RebootKey(b.project)).Result()
	for _, absent := range []string{`"why"`, `"n"`, `"max"`, `"window"`} {
		if strings.Contains(raw, absent) {
			t.Fatalf("intentional carries %s: %s", absent, raw)
		}
	}
	// Delete: under the lease, and only the boot it names.
	if err := b.FencedDelReboot(ctx, "tok", "A"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Reboot(ctx); !ok {
		t.Fatal("another boot's delete removed the record")
	}
	if err := b.FencedDelReboot(ctx, "other", "B"); err != ErrNotHolder {
		t.Fatalf("another token deleted: %v", err)
	}
	if err := b.FencedDelReboot(ctx, "tok", "B"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Reboot(ctx); ok {
		t.Fatal("not deleted")
	}
	if c, kind := ClassOf(b.project, RebootKey(b.project)); c != KeyRuntime || kind != "reboot" {
		t.Fatalf("class %v %q", c, kind)
	}
}

// reboot-resume §3.2: the dispatcher's suspend, under the lease, with the
// fields agentbus resume reads; an existing suspend (the person's) is kept.
func TestFencedSuspend(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	spec := SuspendSpec{By: "dispatch", Notify: "master", Reason: "stopped after 4 unplanned restarts of this machine in 24 h",
		Host: "h", Boot: "B", Services: map[string]bool{"dispatch": true, "missions": false, "holder": true}}
	if _, _, err := b.FencedSuspend(ctx, "other", spec); err != ErrNotHolder {
		t.Fatalf("another token suspended: %v", err)
	}
	p, existed, err := b.FencedSuspend(ctx, "tok", spec)
	if err != nil || existed || !p.Suspended() || p.By != "dispatch" || p.Notify != "master" || p.Reason != spec.Reason ||
		p.Host != "h" || p.Boot != "B" || !p.Services["holder"] || p.Services["missions"] || p.At <= 0 || p.Until <= p.At {
		t.Fatalf("suspend %+v existed %v err %v", p, existed, err)
	}
	spec.Reason = "another"
	p2, existed, err := b.FencedSuspend(ctx, "tok", spec)
	if err != nil || !existed || p2.Reason != p.Reason {
		t.Fatalf("an existing suspend replaced: %+v %v %v", p2, existed, err)
	}
}
