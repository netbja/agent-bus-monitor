package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const runGap = 15 * time.Minute

var h1, h2, h3, h4 = strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)

func runPass(t *testing.T, b *Bus, known bool, heads ...string) RunRecord {
	t.Helper()
	rec, _, err := b.FencedRunPass(context.Background(), "tok", heads, known, runGap)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// moveSeen sets the run's seen_at and started_at back by d, as if the last
// pass was d ago and the run began d earlier.
func moveSeen(t *testing.T, b *Bus, d time.Duration) {
	t.Helper()
	ctx := context.Background()
	rec, err := b.Run(ctx)
	if err != nil || rec == nil {
		t.Fatalf("Run = %v, %v", rec, err)
	}
	if err := b.r.HSet(ctx, RunKey(b.project), "seen_at", rec.SeenAt-d.Milliseconds(), "started_at", rec.StartedAt-d.Milliseconds()).Err(); err != nil {
		t.Fatal(err)
	}
}

// run-budget §2: the first pass with heads known starts a run with every
// head given as its baseline, counting none; a later pass counts each new
// head once; a pass without the heads known starts nothing.
func TestRunStartsWithABaselineAndCountsOnce(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if _, _, err := b.FencedRunPass(ctx, "bad", []string{h1}, true, runGap); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v, want ErrNotHolder", err)
	}
	if _, started, err := b.FencedRunPass(ctx, "tok", nil, false, runGap); err != nil || started {
		t.Fatalf("heads unknown = %v, %v: want no run", started, err)
	}
	if rec, err := b.Run(ctx); err != nil || rec != nil {
		t.Fatalf("a run without a baseline: %+v, %v", rec, err)
	}
	rec, started, err := b.FencedRunPass(ctx, "tok", []string{h1, h2}, true, runGap)
	if err != nil || !started {
		t.Fatalf("first pass = %v, %v", started, err)
	}
	if rec.State != RunRunning || rec.Deliveries != 0 || rec.StartedAt == 0 || rec.Since != rec.StartedAt || rec.SeenAt != rec.StartedAt || rec.Drains != 0 {
		t.Fatalf("started %+v", rec)
	}
	rec = runPass(t, b, true, h1, h2, h3)
	if rec.Deliveries != 1 {
		t.Fatalf("a new head: %d deliveries, want 1", rec.Deliveries)
	}
	rec = runPass(t, b, true, h1, h3)
	if rec.Deliveries != 1 {
		t.Fatalf("the same heads again: %d deliveries, want 1", rec.Deliveries)
	}
	rec = runPass(t, b, false)
	if rec.Deliveries != 1 {
		t.Fatalf("heads unknown: %d deliveries, want 1", rec.Deliveries)
	}
	if err := b.ResetRun(ctx); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.Run(ctx); rec != nil {
		t.Fatalf("after reset: %+v", rec)
	}
	rec = runPass(t, b, true, h1, h2, h3)
	if rec.Deliveries != 0 {
		t.Fatalf("reset: the next run's baseline holds every merged head, got %d", rec.Deliveries)
	}
	if rec = runPass(t, b, true, h1, h2, h3, h4); rec.Deliveries != 1 {
		t.Fatalf("after reset, the next merge: %d, want 1", rec.Deliveries)
	}
}

// run-budget §2: a gap under three passes is run time; a longer one (a host
// asleep) and every interval while the project is paused are added to
// paused_ms; all Redis time.
func TestRunPausedTime(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	runPass(t, b, true)
	moveSeen(t, b, 10*time.Minute)
	if rec := runPass(t, b, true); rec.PausedMs != 0 {
		t.Fatalf("a 10m gap: paused %dms, want run time", rec.PausedMs)
	}
	moveSeen(t, b, 8*time.Hour)
	rec := runPass(t, b, true)
	if rec.PausedMs < (8 * time.Hour).Milliseconds() {
		t.Fatalf("an 8h gap: paused %dms, want at least 8h", rec.PausedMs)
	}
	before := rec.PausedMs
	if _, err := b.SetPause(ctx, "master", "master", "quota", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	moveSeen(t, b, 4*time.Minute)
	rec = runPass(t, b, true)
	if got := rec.PausedMs - before; got < (4 * time.Minute).Milliseconds() {
		t.Fatalf("paused 4m: added %dms", got)
	}
	// A suspend counts as paused (a pass may still run during it).
	if err := b.r.Set(ctx, PauseKey(b.project), `{"kind":"suspend","until":0}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	before = rec.PausedMs
	moveSeen(t, b, 4*time.Minute)
	rec = runPass(t, b, true)
	if got := rec.PausedMs - before; got < (4 * time.Minute).Milliseconds() {
		t.Fatalf("suspended 4m: added %dms", got)
	}
	// A pause that ended within the interval: paused at its start, so the
	// whole interval is paused (to within one pass, §2).
	before = rec.PausedMs
	moveSeen(t, b, 4*time.Minute)
	if rec, err := b.Run(ctx); err != nil || b.r.Set(ctx, PauseKey(b.project), fmt.Sprintf(`{"until":%d}`, rec.SeenAt+time.Minute.Milliseconds()), 0).Err() != nil {
		t.Fatalf("pause ended in the interval: %v", err)
	}
	rec = runPass(t, b, true)
	if got := rec.PausedMs - before; got < (4 * time.Minute).Milliseconds() {
		t.Fatalf("a pause ended within the interval: added %dms, want the interval", got)
	}
	if err := b.r.Del(ctx, PauseKey(b.project)).Err(); err != nil {
		t.Fatal(err)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := rec.Elapsed(now.UnixMilli()); e < 10*time.Minute || e > 11*time.Minute {
		t.Fatalf("elapsed %s: want about 10m, the one gap counted as run time", e)
	}
}
