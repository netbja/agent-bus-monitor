package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// audit step 2 (spec §2): the dispatcher's quota pause writes its audit line
// inside its fenced script, once per pause: the pass that refreshes the same
// pause writes none, and a call without the lease neither pauses nor writes.
func TestQuotaPauseAudits(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project), PauseKey(b.project)) })
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	until := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := b.FencedQuotaPause(ctx, "stale", "anthropic", "master", until); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
	if n := auditLen(t, b); n != 0 {
		t.Fatalf("a pause without the lease wrote %d audit lines", n)
	}
	for range 3 { // the first pass pauses, the next ones refresh the same pause
		if wrote, err := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", until); err != nil || !wrote {
			t.Fatalf("pause: %v %v", wrote, err)
		}
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 1 {
		t.Fatalf("%d audit lines for one pause, want 1: %+v", len(es), es)
	}
	e := es[0]
	if e.Action != "quota-pause" || e.Actor != "dispatch" || e.Target != "anthropic" || e.Outcome != AuditOK ||
		e.Detail != "paused until 2026-11-25T18:00:00Z, master told" {
		t.Fatalf("the pause's line %+v", e)
	}
}

// A repause (master moved to another provider, held too) is a new quota
// pause: one line naming the new provider and the one it replaces.
func TestQuotaRepauseAudits(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project), PauseKey(b.project)) })
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	until := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", until); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedQuotaRepause(ctx, "stale", "anthropic", "openai", until); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
	if ok, err := b.FencedQuotaRepause(ctx, "tok", "anthropic", "openai", until); err != nil || !ok {
		t.Fatalf("repause: %v %v", ok, err)
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 2 || es[0].Action != "quota-pause" || es[0].Target != "openai" ||
		es[0].Detail != "paused until 2026-11-25T18:00:00Z, replacing anthropic's pause" {
		t.Fatalf("lines %+v", es)
	}
}
