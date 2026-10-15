package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPauseSetReadResume(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	if p, err := b.Pause(ctx); err != nil || p != nil {
		t.Fatalf("fresh project paused: %v %v", p, err)
	}
	if _, err := b.SetPause(ctx, "master", "master", "quota", time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("a pause in the past was accepted")
	}
	until := time.Now().Add(2 * time.Hour)
	if _, err := b.SetPause(ctx, "master", "master", "quota", until); err != nil {
		t.Fatal(err)
	}
	p, err := b.Pause(ctx)
	if err != nil || p == nil || p.Reason != "quota" || p.Notify != "master" || p.Until != until.UnixMilli() {
		t.Fatalf("pause = %+v, %v", p, err)
	}
	ok, err := b.Resume(ctx, "bernard", "go on")
	if err != nil || !ok {
		t.Fatalf("resume = %v, %v", ok, err)
	}
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatal("still paused")
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	if len(evs) == 0 || evs[len(evs)-1].Target != "master" || !strings.Contains(evs[len(evs)-1].Message, "quota") {
		t.Fatalf("resume directive = %+v", evs)
	}
	if ok, _ := b.Resume(ctx, "bernard", ""); ok {
		t.Fatal("a second resume found a pause")
	}
}

func TestFencedResumeOnlyWhenDueAndHeld(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project()), DispatchKey(b.Project())) })
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := b.SetPause(ctx, "master", "lead", "quota", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, err := b.FencedResume(ctx, "tok", "dispatch"); err != nil || id != "" {
		t.Fatalf("not due: %q %v", id, err)
	}
	if _, err := b.FencedResume(ctx, "other", "dispatch"); err != ErrNotHolder {
		t.Fatalf("wrong token: %v", err)
	}
	// Due: rewrite the record with a deadline already past.
	b.r.Set(ctx, PauseKey(b.Project()), `{"until":1,"reason":"quota","by":"master","notify":"lead","at":1}`, 0)
	id, err := b.FencedResume(ctx, "tok", "dispatch")
	if err != nil || id == "" {
		t.Fatalf("due: %q %v", id, err)
	}
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatal("pause kept after the resume")
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	if last := evs[len(evs)-1]; last.Target != "lead" || last.From != "dispatch" {
		t.Fatalf("resume entry %+v", last)
	}
	if id, _ := b.FencedResume(ctx, "tok", "dispatch"); id != "" {
		t.Fatal("resumed twice")
	}
}
