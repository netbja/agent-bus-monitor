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

// The run's counts are parsed as int without a narrowing conversion
// (CodeQL): a value out of range, negative or not a number reads 0.
func TestParseRunCounts(t *testing.T) {
	for raw, want := range map[string]int{"7": 7, "0": 0, "-5": 0, "99999999999999999999999": 0, "x": 0, "": 0} {
		r := parseRun(map[string]string{"deliveries": raw, "drains": raw})
		if r.Deliveries != want || r.Drains != want {
			t.Errorf("%q: deliveries %d, drains %d, want %d", raw, r.Deliveries, r.Drains, want)
		}
	}
}

func runWithLease(t *testing.T) (*Bus, context.Context) {
	t.Helper()
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	runPass(t, b, true)
	return b, ctx
}

// run-budget §3: a limit drains the run once, with the episode of its marks
// <started_at>:<drains>; a second limit while draining changes nothing; only
// the lease holder writes.
func TestRunDrain(t *testing.T) {
	b, ctx := runWithLease(t)
	if _, _, err := b.FencedRunDrain(ctx, "bad", RunWhyDuration); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
	ep, drained, err := b.FencedRunDrain(ctx, "tok", RunWhyDuration)
	rec, _ := b.Run(ctx)
	if err != nil || !drained || rec.State != RunDraining || rec.Why != RunWhyDuration || rec.Drains != 1 ||
		ep != fmt.Sprintf("%d:1", rec.StartedAt) || rec.Since == 0 {
		t.Fatalf("drain = %q %v %v, record %+v", ep, drained, err, rec)
	}
	if _, drained, err := b.FencedRunDrain(ctx, "tok", RunWhyDeliveries); err != nil || drained {
		t.Fatalf("a second limit while draining: %v %v", drained, err)
	}
	if rec, _ := b.Run(ctx); rec.Why != RunWhyDuration || rec.Drains != 1 {
		t.Fatalf("record changed: %+v", rec)
	}
}

// §3, §4: a quota drain returns to running on its release; a limit reached
// during it replaces why (no new drain), and the release then leaves the run
// draining; a second drain after a release is a new episode.
func TestRunQuotaDrainAndRelease(t *testing.T) {
	b, ctx := runWithLease(t)
	q := RunWhyQuota("anthropic")
	if _, drained, err := b.FencedRunDrain(ctx, "tok", q); err != nil || !drained {
		t.Fatalf("quota drain: %v %v", drained, err)
	}
	if back, err := b.FencedRunQuotaRelease(ctx, "tok", "openai"); err != nil || back {
		t.Fatalf("another provider's release: %v %v", back, err)
	}
	if back, err := b.FencedRunQuotaRelease(ctx, "tok", "anthropic"); err != nil || !back {
		t.Fatalf("release: %v %v", back, err)
	}
	if rec, _ := b.Run(ctx); rec.State != RunRunning || rec.Why != "" {
		t.Fatalf("after the release: %+v", rec)
	}
	ep, drained, err := b.FencedRunDrain(ctx, "tok", q)
	rec, _ := b.Run(ctx)
	if err != nil || !drained || rec.Drains != 2 || ep != fmt.Sprintf("%d:2", rec.StartedAt) {
		t.Fatalf("second drain %q %v %v %+v", ep, drained, err, rec)
	}
	if _, drained, err := b.FencedRunDrain(ctx, "tok", RunWhyDeliveries); err != nil || drained {
		t.Fatalf("a limit during a quota drain is no new drain: %v %v", drained, err)
	}
	if rec, _ := b.Run(ctx); rec.Why != RunWhyDeliveries || rec.Drains != 2 || rec.State != RunDraining {
		t.Fatalf("why not replaced: %+v", rec)
	}
	if back, err := b.FencedRunQuotaRelease(ctx, "tok", "anthropic"); err != nil || back {
		t.Fatalf("the quota release ran past a real limit: %v %v", back, err)
	}
}

// §3: the stop writes a kind-run pause a year ahead (a person's or a quota
// pause replaced, never a suspend), which no quota release lifts and run
// reset does.
func TestRunStopAndReset(t *testing.T) {
	b, ctx := runWithLease(t)
	if stopped, err := b.FencedRunStop(ctx, "tok", "master"); err != nil || stopped {
		t.Fatalf("a running run stopped: %v %v", stopped, err)
	}
	if _, _, err := b.FencedRunDrain(ctx, "tok", RunWhyDeliveries); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedQuotaPause(ctx, "tok", "anthropic", "master", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if stopped, err := b.FencedRunStop(ctx, "tok", "master"); err != nil || !stopped {
		t.Fatalf("stop: %v %v", stopped, err)
	}
	p, err := b.Pause(ctx)
	if err != nil || p == nil || p.Kind != PauseKindRun || p.UntilTime().Before(time.Now().Add(300*24*time.Hour)) || QuotaPauseProvider(p) != "" {
		t.Fatalf("pause %+v %v", p, err)
	}
	if rec, _ := b.Run(ctx); rec.State != RunStopped {
		t.Fatalf("record %+v", rec)
	}
	if ok, err := b.FencedQuotaUnpause(ctx, "tok", "anthropic", "dispatch"); err != nil || ok {
		t.Fatalf("a quota release lifted the run's pause: %v %v", ok, err)
	}
	if err := b.ResetRun(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := b.Pause(ctx); p != nil {
		t.Fatalf("reset left the run's pause: %+v", p)
	}
	if rec, _ := b.Run(ctx); rec != nil {
		t.Fatalf("reset left the run: %+v", rec)
	}
}

// A suspend is never replaced by the stop; reset never lifts a pause other
// than the run's.
func TestRunStopKeepsASuspendResetKeepsOtherPauses(t *testing.T) {
	b, ctx := runWithLease(t)
	if _, _, err := b.FencedRunDrain(ctx, "tok", RunWhyDuration); err != nil {
		t.Fatal(err)
	}
	b.r.Set(ctx, PauseKey(b.project), `{"until":1,"by":"charlie","notify":"master","kind":"suspend"}`, 0)
	if stopped, err := b.FencedRunStop(ctx, "tok", "master"); err != nil || !stopped {
		t.Fatalf("stop: %v %v", stopped, err)
	}
	if p, _ := b.Pause(ctx); p == nil || p.Kind != PauseKindSuspend {
		t.Fatalf("suspend replaced: %+v", p)
	}
	if err := b.ResetRun(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := b.Pause(ctx); p == nil || p.Kind != PauseKindSuspend {
		t.Fatalf("reset lifted a suspend: %+v", p)
	}
}

// run-budget §3: while the run drains, a new task is refused to an agent and
// allowed to the person; a running or absent run refuses nobody.
func TestRequestRefusedWhileDraining(t *testing.T) {
	b, ctx := runWithLease(t)
	if _, err := b.Request(ctx, "t1", "master", "coder", "", "x", 0); err != nil {
		t.Fatalf("running: %v", err)
	}
	if _, _, err := b.FencedRunDrain(ctx, "tok", RunWhyDuration); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "t2", "master", "coder", "", "x", 0); !errors.Is(err, ErrRunDraining) {
		t.Fatalf("an agent's task while draining: %v, want ErrRunDraining", err)
	}
	if board, _ := b.Board(ctx); board["t2"].State != "" {
		t.Fatalf("refused task written: %+v", board["t2"])
	}
	if _, err := b.RequestBy(ctx, "t3", "charlie", "coder", "", "x", 0, true); err != nil {
		t.Fatalf("the person's task while draining: %v", err)
	}
}
