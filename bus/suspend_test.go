package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func suspendSpec() SuspendSpec {
	return SuspendSpec{By: "charlie", Notify: "master", Reason: "OS updates", Host: "laptop", Boot: "b1",
		Services: map[string]bool{"dispatch": true, "missions": false}}
}

func TestSuspendWritesTheRecord(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	before := time.Now()
	rec, raw, existed, err := b.Suspend(ctx, suspendSpec())
	if err != nil || existed {
		t.Fatalf("Suspend = %v, existed %v", err, existed)
	}
	if !rec.Suspended() || rec.By != "charlie" || rec.Notify != "master" || rec.Reason != "OS updates" ||
		rec.Host != "laptop" || rec.Boot != "b1" || !rec.Services["dispatch"] || rec.Services["missions"] || rec.Prev != nil {
		t.Fatalf("record = %+v", rec)
	}
	if rec.At < before.Add(-time.Minute).UnixMilli() || rec.Until-rec.At != suspendHorizon.Milliseconds() {
		t.Fatalf("at %d until %d: want until = at + %v", rec.At, rec.Until, suspendHorizon)
	}
	if got, _ := b.r.Get(ctx, PauseKey(b.Project())).Result(); got != raw {
		t.Fatalf("raw = %q, stored %q", raw, got)
	}
	p, err := b.Pause(ctx)
	if err != nil || p == nil || !p.Suspended() || len(p.Services) != 2 {
		t.Fatalf("Pause = %+v, %v", p, err)
	}
	// An older reader, which knows none of the new fields, still decodes it.
	var old struct {
		Until  int64  `json:"until"`
		Reason string `json:"reason"`
		By     string `json:"by"`
		Notify string `json:"notify"`
		At     int64  `json:"at"`
	}
	if err := json.Unmarshal([]byte(raw), &old); err != nil || old.Until != rec.Until || old.Notify != "master" {
		t.Fatalf("old reader: %+v, %v", old, err)
	}
}

func TestSuspendLeavesAnExistingSuspend(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	first, raw, _, err := b.Suspend(ctx, suspendSpec())
	if err != nil {
		t.Fatal(err)
	}
	again := suspendSpec()
	again.By, again.Services = "dana", map[string]bool{}
	rec, raw2, existed, err := b.Suspend(ctx, again)
	if err != nil || !existed || raw2 != raw || rec.By != first.By || !rec.Services["dispatch"] {
		t.Fatalf("second Suspend = %+v existed %v %v; want the first record kept", rec, existed, err)
	}
}

func TestSuspendKeepsATimedPauseAsPrev(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	until := time.Now().Add(2 * time.Hour)
	if _, err := b.SetPause(ctx, "dana", "master", "lunch", until); err != nil {
		t.Fatal(err)
	}
	rec, _, _, err := b.Suspend(ctx, suspendSpec())
	if err != nil || rec.Prev == nil || rec.Prev.Reason != "lunch" || rec.Prev.By != "dana" ||
		rec.Prev.Until != until.UnixMilli() || rec.Prev.Suspended() {
		t.Fatalf("record = %+v (prev %+v), %v", rec, rec.Prev, err)
	}
}

func TestATimedPauseNeverTakesASuspend(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project()), DispatchKey(b.Project())) })
	_, raw, _, err := b.Suspend(ctx, suspendSpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetPause(ctx, "dana", "master", "x", time.Now().Add(time.Hour)); !errors.Is(err, ErrSuspended) {
		t.Fatalf("SetPause on a suspend = %v, want ErrSuspended", err)
	}
	if ok, err := b.Resume(ctx, "dana", ""); ok || !errors.Is(err, ErrSuspended) {
		t.Fatalf("Resume on a suspend = %v, %v; want ErrSuspended", ok, err)
	}
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	// A suspend whose reason reads like a quota pause is still not the dispatcher's to take.
	spec := suspendSpec()
	spec.Reason = QuotaPauseReason("anthropic")
	b.r.Del(ctx, PauseKey(b.Project()))
	if _, raw, _, err = b.Suspend(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", time.Now().Add(time.Hour).UnixMilli()); err != nil || ok {
		t.Fatalf("FencedQuotaPause over a suspend = %v, %v", ok, err)
	}
	if ok, err := b.FencedQuotaUnpause(ctx, "tok", "anthropic", "dispatch"); err != nil || ok {
		t.Fatalf("FencedQuotaUnpause of a suspend = %v, %v", ok, err)
	}
	// However due its Until, the dispatcher never ends a suspend.
	var rec map[string]any
	_ = json.Unmarshal([]byte(raw), &rec)
	rec["until"] = 1
	past, _ := json.Marshal(rec)
	b.r.Set(ctx, PauseKey(b.Project()), past, 0)
	if id, err := b.FencedResume(ctx, "tok", "dispatch"); err != nil || id != "" {
		t.Fatalf("FencedResume of a due suspend = %q, %v", id, err)
	}
	if p, _ := b.Pause(ctx); p == nil || !p.Suspended() {
		t.Fatalf("suspend gone: %+v", p)
	}
}

func TestUnsuspendLiftsOrRestoresPrev(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })

	// No previous pause: lifted, and the notify target gets the resume directive.
	_, raw, _, err := b.Suspend(ctx, suspendSpec())
	if err != nil {
		t.Fatal(err)
	}
	restored, id, err := b.Unsuspend(ctx, raw, "charlie", "Open: 2 asks.")
	if err != nil || restored || id == "" {
		t.Fatalf("Unsuspend = %v %q %v", restored, id, err)
	}
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatalf("still paused: %+v", p)
	}
	evs, _, _ := b.Recent(ctx, []string{"cmd"}, 5)
	last := evs[len(evs)-1]
	if last.ID != id || last.Target != "master" || last.From != "charlie" || !strings.Contains(last.Message, "Open: 2 asks.") || last.NoWake || !last.Human {
		t.Fatalf("resume entry %+v", last)
	}
	// A record changed since it was read is left alone.
	if _, _, err := b.Unsuspend(ctx, raw, "charlie", ""); !errors.Is(err, ErrPauseChanged) {
		t.Fatalf("Unsuspend of a gone record = %v, want ErrPauseChanged", err)
	}

	// A timed pause still due comes back, with no directive.
	until := time.Now().Add(time.Hour)
	if _, err := b.SetPause(ctx, "dana", "master", "lunch", until); err != nil {
		t.Fatal(err)
	}
	if _, raw, _, err = b.Suspend(ctx, suspendSpec()); err != nil {
		t.Fatal(err)
	}
	n := len(mustRecent(t, b))
	restored, id, err = b.Unsuspend(ctx, raw, "charlie", "")
	if err != nil || !restored || id != "" {
		t.Fatalf("Unsuspend over a due pause = %v %q %v", restored, id, err)
	}
	p, _ := b.Pause(ctx)
	if p == nil || p.Suspended() || p.Reason != "lunch" || p.Until != until.UnixMilli() {
		t.Fatalf("pause after = %+v", p)
	}
	if got := len(mustRecent(t, b)); got != n {
		t.Fatalf("a directive was sent while the previous pause came back")
	}

	// A previous pause already past: lifted, as with none.
	b.r.Del(ctx, PauseKey(b.Project()))
	b.r.Set(ctx, PauseKey(b.Project()), `{"until":1,"reason":"lunch","by":"dana","notify":"master","at":1}`, 0)
	if _, raw, _, err = b.Suspend(ctx, suspendSpec()); err != nil {
		t.Fatal(err)
	}
	if restored, id, err := b.Unsuspend(ctx, raw, "charlie", ""); err != nil || restored || id == "" {
		t.Fatalf("Unsuspend over a past pause = %v %q %v", restored, id, err)
	}
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatalf("still paused: %+v", p)
	}
}

func TestUnsuspendRefusesATimedPause(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	if _, err := b.SetPause(ctx, "dana", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.r.Get(ctx, PauseKey(b.Project())).Result()
	if _, _, err := b.Unsuspend(ctx, raw, "charlie", ""); !errors.Is(err, ErrNotSuspended) {
		t.Fatalf("Unsuspend of a timed pause = %v, want ErrNotSuspended", err)
	}
	if p, _ := b.Pause(ctx); p == nil || p.Reason != "lunch" {
		t.Fatalf("pause = %+v", p)
	}
}

func TestReserveWakeRefusedUnderAPause(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	if ok, _ := b.AcquireDispatch(ctx, "tok", 15*time.Second); !ok {
		t.Fatal("AcquireDispatch failed")
	}
	next := WakeRecord{State: "prompting", Tries: 1, HighID: "1-0", Reason: "obligation"}
	for _, pause := range []func(){
		func() {
			if _, err := b.SetPause(ctx, "dana", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if _, _, _, err := b.Suspend(ctx, suspendSpec()); err != nil {
				t.Fatal(err)
			}
		},
	} {
		b.r.Del(ctx, PauseKey(b.Project()), WakeKey(b.Project(), "coder"))
		pause()
		if _, ok, err := b.ReserveWake(ctx, "coder", "tok", next, time.Millisecond, time.Second); ok || !errors.Is(err, ErrPaused) {
			t.Fatalf("ReserveWake under a pause = %v, %v; want ErrPaused", ok, err)
		}
		if _, exists, _ := b.Wake(ctx, "coder"); exists {
			t.Fatal("a wake record was written under a pause")
		}
		// The lease still comes first.
		if _, _, err := b.ReserveWake(ctx, "coder", "other", next, time.Millisecond, time.Second); !errors.Is(err, ErrNotHolder) {
			t.Fatalf("lost lease under a pause = %v, want ErrNotHolder", err)
		}
	}
	b.r.Del(ctx, PauseKey(b.Project()))
	if _, ok, err := b.ReserveWake(ctx, "coder", "tok", next, time.Millisecond, time.Second); !ok || err != nil {
		t.Fatalf("ReserveWake with no pause = %v, %v", ok, err)
	}
}

func mustRecent(t *testing.T, b *Bus) []Event {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"cmd"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// Every accepted suspend stays resumable (reviewer-codex): a reason at the
// limit, multibyte or not, and a summary or cancel note of any length never
// stop the lift; the directive is cut to the limit, visibly.
func TestUnsuspendNeverBlockedByTheDirectiveLength(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, PauseKey(b.Project())) })
	for _, reason := range []string{strings.Repeat("x", CmdMaxRunes), strings.Repeat("é", CmdMaxRunes), strings.Repeat("界", CmdMaxRunes)} {
		for name, end := range map[string]func(raw string) (bool, string, error){
			"unsuspend": func(raw string) (bool, string, error) {
				return b.Unsuspend(ctx, raw, "charlie", strings.Repeat("summary ", CmdMaxRunes))
			},
			"cancel": func(raw string) (bool, string, error) {
				return b.SuspendCancel(ctx, raw, "charlie", strings.Repeat("ç", CmdMaxRunes))
			},
		} {
			b.r.Del(ctx, PauseKey(b.Project()))
			spec := suspendSpec()
			spec.Reason = reason
			_, raw, _, err := b.Suspend(ctx, spec)
			if err != nil {
				t.Fatalf("Suspend with a %d-rune reason: %v", CmdMaxRunes, err)
			}
			restored, id, err := end(raw)
			if err != nil || restored || id == "" {
				t.Fatalf("%s: %v %q %v", name, restored, id, err)
			}
			if p, _ := b.Pause(ctx); p != nil {
				t.Fatalf("%s: still paused", name)
			}
			evs := mustRecent(t, b)
			msg := evs[len(evs)-1].Message
			if n := len([]rune(msg)); n > CmdMaxRunes || !strings.HasSuffix(msg, directiveCut) {
				t.Fatalf("%s: directive of %d runes, ends %q", name, n, msg[max(0, len(msg)-40):])
			}
		}
	}
	// A previous pause still due comes back, whatever the directive would have been.
	b.r.Del(ctx, PauseKey(b.Project()))
	if _, err := b.SetPause(ctx, "dana", "master", strings.Repeat("é", CmdMaxRunes), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	spec := suspendSpec()
	spec.Reason = strings.Repeat("界", CmdMaxRunes)
	_, raw, _, err := b.Suspend(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if restored, _, err := b.Unsuspend(ctx, raw, "charlie", strings.Repeat("x", 3*CmdMaxRunes)); err != nil || !restored {
		t.Fatalf("prev not restored: %v %v", restored, err)
	}
}

func TestFitDirective(t *testing.T) {
	short := "Project resumed."
	if got := fitDirective(short); got != short {
		t.Fatalf("short text changed: %q", got)
	}
	exact := strings.Repeat("é", CmdMaxRunes)
	if got := fitDirective(exact); got != exact {
		t.Fatal("a text at the limit was cut")
	}
	long := strings.Repeat("界", CmdMaxRunes+1)
	got := fitDirective(long)
	if len([]rune(got)) != CmdMaxRunes || !strings.HasSuffix(got, directiveCut) || ValidateText(got, CmdMaxRunes) != nil {
		t.Fatalf("cut text: %d runes, valid %v", len([]rune(got)), ValidateText(got, CmdMaxRunes))
	}
	if got := fitDirective("bad \xff byte"); ValidateText(got, CmdMaxRunes) != nil {
		t.Fatalf("invalid UTF-8 kept: %q", got)
	}
}
