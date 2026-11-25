package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// audit step 2 (spec §2): pause, resume and suspend write their line after
// the act, wherever they run (the CLI, busmon); a refused one writes
// outcome refused; nothing done, nothing written. The reason, free text,
// never enters the trail.
func TestPauseResumeSuspendAudit(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project), PauseKey(b.project)) })
	lines := func() []AuditEntry { es, _ := b.AuditEntries(ctx, 0); return es }

	if ok, err := b.Resume(ctx, "charlie", ""); err != nil || ok {
		t.Fatalf("resume with nothing paused: %v %v", ok, err)
	}
	if len(lines()) != 0 {
		t.Fatal("a resume that did nothing wrote a line")
	}
	until := time.Now().Add(2 * time.Hour)
	if _, err := b.SetPause(ctx, "charlie", "master", "lunch, secret plans", until); err != nil {
		t.Fatal(err)
	}
	if es := lines(); len(es) != 1 || es[0].Action != "pause" || es[0].Actor != "charlie" || es[0].Target != b.project ||
		es[0].Outcome != AuditOK || es[0].Detail != "until "+until.UTC().Format(time.RFC3339) {
		t.Fatalf("the pause's line %+v", es)
	}
	if ok, err := b.Resume(ctx, "charlie", "go on"); err != nil || !ok {
		t.Fatalf("resume: %v %v", ok, err)
	}
	if es := lines(); len(es) != 2 || es[0].Action != "resume" || es[0].Actor != "charlie" || es[0].Detail != "the pause lifted" {
		t.Fatalf("the resume's line %+v", es)
	}

	if _, _, already, err := b.Suspend(ctx, suspendSpec()); err != nil || already {
		t.Fatalf("suspend: already %v %v", already, err)
	}
	if es := lines(); len(es) != 3 || es[0].Action != "suspend" || es[0].Actor != "charlie" || es[0].Outcome != AuditOK {
		t.Fatalf("the suspend's line %+v", es)
	}
	if _, err := b.SetPause(ctx, "charlie", "master", "x", until); !errors.Is(err, ErrSuspended) {
		t.Fatalf("a pause while suspended: %v", err)
	}
	if es := lines(); len(es) != 4 || es[0].Action != "pause" || es[0].Outcome != AuditRefused || es[0].Detail != "the project is suspended" {
		t.Fatalf("the refused pause's line %+v", es)
	}
	if _, _, already, _ := b.Suspend(ctx, suspendSpec()); !already {
		t.Fatal("a second suspend was not refused")
	}
	if es := lines(); len(es) != 5 || es[0].Action != "suspend" || es[0].Outcome != AuditRefused {
		t.Fatalf("the second suspend's line %+v", es)
	}
	raw, _ := b.PauseRaw(ctx)
	if _, _, err := b.Unsuspend(ctx, raw, "charlie", ""); err != nil {
		t.Fatal(err)
	}
	if es := lines(); len(es) != 6 || es[0].Action != "resume" || es[0].Detail != "the suspend ended" {
		t.Fatalf("the unsuspend's line %+v", es)
	}
	for _, e := range lines() {
		if e.Detail == "lunch, secret plans" || e.Target == "lunch, secret plans" {
			t.Fatalf("the reason in the trail: %+v", e)
		}
	}
	if c, _ := b.AuditVerify(ctx); !c.OK || c.Entries != 6 {
		t.Fatalf("chain %+v", c)
	}
}
