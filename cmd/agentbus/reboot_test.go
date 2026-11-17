package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/lifecycle"
	"github.com/netbja/agent-bus-monitor/service"
)

// judgeWorld is a project on the disposable broker, its lease held as
// "tok", its state directory, and a judge on boot B1 whose clock, holder
// and repository are the test's.
type judgeWorld struct {
	t        *testing.T
	b        *bus.Bus
	dir      string
	j        *rebootJudge
	now      time.Time
	readyAt  time.Time // the holder answers from then on (zero: never)
	polls    int
	repoWhy  string
	set      rebootSettings
	repoRuns int
}

var jT0 = time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)

func newJudgeWorld(t *testing.T) *judgeWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "rj"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	w := &judgeWorld{t: t, b: b, dir: filepath.Join(t.TempDir(), "state"), now: jT0,
		set: rebootSettings{Restart: true, Max: 3, Window: 24 * time.Hour, SelfRepair: "on"}}
	w.j = &rebootJudge{b: b, dir: w.dir, boot: "B1", host: "h1",
		escalate: func() string { return "master" },
		settings: func() rebootSettings { return w.set },
		holderReady: func(context.Context) bool {
			w.polls++
			return !w.readyAt.IsZero() && !w.now.Before(w.readyAt)
		},
		repo:     func(context.Context) string { w.repoRuns++; return w.repoWhy },
		services: func() map[string]bool { return map[string]bool{"dispatch": true, "missions": false, "holder": true} },
		now:      func() time.Time { return w.now },
		sleep: func(ctx context.Context, d time.Duration) error {
			w.now = w.now.Add(d)
			return ctx.Err()
		},
		log: os.Stderr}
	return w
}

// before writes the run record a previous boot (B0) left.
func (w *judgeWorld) before(state, cause string) {
	w.t.Helper()
	if err := lifecycle.Up(w.dir, "B0", jT0); err != nil { // the counter created, as up does
		w.t.Fatal(err)
	}
	switch {
	case state == lifecycle.Stopped && cause == lifecycle.CauseSignal:
		if err := lifecycle.Signalled(w.dir, "B0", jT0); err != nil {
			w.t.Fatal(err)
		}
	case state == lifecycle.Stopped:
		if err := lifecycle.Stopping(w.dir, "B0", cause, jT0); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *judgeWorld) run() error { return w.j.run(context.Background(), "tok") }

func (w *judgeWorld) record() (bus.RebootRecord, bool) {
	w.t.Helper()
	r, ok, err := w.b.Reboot(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return r, ok
}

func (w *judgeWorld) runRecord() lifecycle.Record {
	w.t.Helper()
	r, err := lifecycle.ReadRun(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *judgeWorld) counter() []lifecycle.Entry {
	w.t.Helper()
	es, err := lifecycle.ReadCounter(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return es
}

func (w *judgeWorld) notices() []string {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"notify"}, 100)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.For == bus.ForPerson {
			out = append(out, e.Message)
		}
	}
	return out
}

func (w *judgeWorld) toMaster() []string {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 100)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == "master" {
			out = append(out, e.Message)
		}
	}
	return out
}

// §2.2: no run record (a first start) or the same boot judges nothing:
// no record, running written for this boot, no wait.
func TestJudgeNone(t *testing.T) {
	w := newJudgeWorld(t)
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.record(); ok || w.polls != 0 || len(w.notices()) != 0 {
		t.Fatalf("a first start judged: polls %d, notices %q", w.polls, w.notices())
	}
	if r := w.runRecord(); r.Boot != "B1" || r.State != lifecycle.Running {
		t.Fatalf("run record %+v", r)
	}
	if err := w.run(); err != nil { // the same boot
		t.Fatal(err)
	}
	if _, ok := w.record(); ok || w.polls != 0 {
		t.Fatal("the same boot judged")
	}
}

// §2.2, H1b 1: intentional and suspended keep their verdict at once,
// whatever the holder does: no wait, no "holder not answering", one notice
// each, running written.
func TestJudgeIntentionalAndSuspendedNeverWait(t *testing.T) {
	for _, c := range []struct {
		cause, verdict, text string
	}{
		{lifecycle.CauseSignal, bus.RebootIntentional, "relaunch: agentbus up"},
		{lifecycle.CauseSuspend, bus.RebootSuspended, "resume: agentbus resume"},
	} {
		w := newJudgeWorld(t)
		w.before(lifecycle.Stopped, c.cause)
		if err := w.run(); err != nil {
			t.Fatal(err)
		}
		rec, ok := w.record()
		if !ok || rec.Verdict != c.verdict || rec.Boot != "B1" || rec.Why != "" || rec.N != 0 || w.polls != 0 || w.now != jT0 {
			t.Fatalf("%s: %+v (polls %d, waited %s)", c.verdict, rec, w.polls, w.now.Sub(jT0))
		}
		if got := w.notices(); len(got) != 1 || !strings.Contains(strings.ToLower(got[0]), c.text) || !strings.Contains(got[0], w.b.Project()) {
			t.Fatalf("%s: notices %q", c.verdict, got)
		}
		if r := w.runRecord(); r.Boot != "B1" || r.State != lifecycle.Running {
			t.Fatalf("%s: run record %+v", c.verdict, r)
		}
	}
}

// H1b 4: an eligible unplanned stop, the holder answering after 30 s:
// restarting exactly once, counted once, the person and master told with
// n of max.
func TestJudgeUnplannedWaitsForTheHolder(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Running, "")
	w.readyAt = jT0.Add(30 * time.Second)
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	rec, ok := w.record()
	if !ok || rec.Verdict != bus.RebootRestarting || rec.N != 1 || rec.Max != 3 || rec.Window != (24*time.Hour).Milliseconds() || rec.Why != "" {
		t.Fatalf("record %+v", rec)
	}
	if d := w.now.Sub(jT0); d < 30*time.Second || d > 32*time.Second {
		t.Fatalf("waited %s", d)
	}
	if es := w.counter(); len(es) != 1 || es[0].Boot != "B1" {
		t.Fatalf("counter %+v", es)
	}
	if got := w.notices(); len(got) != 1 || !strings.Contains(got[0], "1 of 3 in 24 h") {
		t.Fatalf("notices %q", got)
	}
	if got := w.toMaster(); len(got) != 1 || !strings.Contains(got[0], "restarted after an unplanned stop of this machine (1 of 3 in 24 h)") {
		t.Fatalf("master told %q", got)
	}
	// A second start on the same boot changes nothing.
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if es := w.counter(); len(es) != 1 || len(w.notices()) != 1 || len(w.toMaster()) != 1 {
		t.Fatalf("judged twice: counter %+v", es)
	}
}

// H1b 5: the holder never answers: unplanned, holder not answering, after
// 2 minutes, nothing counted, the notice naming the wait.
func TestJudgeHolderNeverAnswers(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Running, "")
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	rec, ok := w.record()
	if !ok || rec.Verdict != bus.RebootUnplanned || rec.Why != "holder not answering" || rec.N != 0 {
		t.Fatalf("record %+v", rec)
	}
	if d := w.now.Sub(jT0); d < rebootHolderWait || d > rebootHolderWait+rebootHolderEvery {
		t.Fatalf("waited %s", d)
	}
	if es := w.counter(); len(es) != 0 || w.repoRuns != 0 {
		t.Fatalf("counted %+v, repo checked %d", es, w.repoRuns)
	}
	if got := w.notices(); len(got) != 1 || !strings.Contains(got[0], "its holder did not answer within 2 minutes of the dispatcher's start") {
		t.Fatalf("notices %q", got)
	}
}

// H1b 3: a dispatcher that stops during the phase writes nothing; the next
// one judges from the start, and counts once.
func TestJudgeRestartDuringThePhase(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Running, "")
	ctx, cancel := context.WithCancel(context.Background())
	w.j.sleep = func(c context.Context, d time.Duration) error {
		w.now = w.now.Add(d)
		if w.now.Sub(jT0) >= 10*time.Second {
			cancel()
		}
		return c.Err()
	}
	if err := w.j.run(ctx, "tok"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a stopped phase: %v", err)
	}
	if _, ok := w.record(); ok {
		t.Fatal("a record written during the phase")
	}
	if r := w.runRecord(); r.Boot != "B0" || r.State != lifecycle.Running {
		t.Fatalf("run record rewritten: %+v", r)
	}
	if es := w.counter(); len(es) != 0 {
		t.Fatalf("counted %+v", es)
	}
	w.j.sleep = func(c context.Context, d time.Duration) error { w.now = w.now.Add(d); return c.Err() }
	w.readyAt = w.now
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := w.record(); rec.Verdict != bus.RebootRestarting || rec.N != 1 || len(w.counter()) != 1 {
		t.Fatalf("second judgement %+v, counter %+v", rec, w.counter())
	}
}

// §3.1 1 and 2: unplanned with the setting off or self-repair off or in
// shadow mode: their own why at once, no wait, nothing counted.
func TestJudgeUnplannedConditionsNeverWait(t *testing.T) {
	for why, set := range map[string]func(*rebootSettings){
		"setting off":                func(s *rebootSettings) { s.Restart = false },
		"self-repair off":            func(s *rebootSettings) { s.SelfRepair = "off" },
		"self-repair in shadow mode": func(s *rebootSettings) { s.SelfRepair = "shadow" },
	} {
		w := newJudgeWorld(t)
		w.before(lifecycle.Running, "")
		set(&w.set)
		if err := w.run(); err != nil {
			t.Fatal(err)
		}
		if rec, _ := w.record(); rec.Verdict != bus.RebootUnplanned || rec.Why != why || w.polls != 0 || len(w.counter()) != 0 {
			t.Fatalf("%s: %+v, polls %d", why, rec, w.polls)
		}
	}
}

// §3.1 3: a damaged repository stops the project: stopped with its why and
// no count, the dispatcher's suspend with §3.2's fields.
func TestJudgeRepositoryCheckStops(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Running, "")
	w.readyAt = jT0
	w.repoWhy = "index.lock present: /w/.git/index.lock"
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	rec, _ := w.record()
	if rec.Verdict != bus.RebootStopped || rec.Why != w.repoWhy || rec.N != 0 || rec.Max != 0 || rec.Window != 0 || len(w.counter()) != 0 {
		t.Fatalf("record %+v, counter %+v", rec, w.counter())
	}
	p, err := w.b.Pause(context.Background())
	if err != nil || p == nil || !p.Suspended() || p.By != "dispatch" || p.Notify != "master" || p.Host != "h1" || p.Boot != "B1" ||
		!strings.Contains(p.Reason, "the repository needs a check ("+w.repoWhy+")") || !p.Services["holder"] || p.Services["missions"] {
		t.Fatalf("suspend %+v %v", p, err)
	}
	if got := w.notices(); len(got) != 1 || !strings.Contains(got[0], "git status, git fsck") {
		t.Fatalf("notices %q", got)
	}
}

// §3.2: the counter lost or garbled never reads as empty: no restart.
func TestJudgeCounterLostOrGarbled(t *testing.T) {
	for why, spoil := range map[string]func(dir string){
		"counter missing":     func(dir string) { os.Remove(filepath.Join(dir, lifecycle.CounterFile)) },
		"counter not written": func(dir string) { os.WriteFile(filepath.Join(dir, lifecycle.CounterFile), []byte("{"), 0o600) },
	} {
		w := newJudgeWorld(t)
		w.before(lifecycle.Running, "")
		w.readyAt = jT0
		spoil(w.dir)
		if err := w.run(); err != nil {
			t.Fatal(err)
		}
		if rec, _ := w.record(); rec.Verdict != bus.RebootUnplanned || rec.Why != why {
			t.Fatalf("%s: %+v", why, rec)
		}
	}
}

// §3.2: the fourth restart in the window stops the project, counted, with
// n, max and window; a suspend of the person's is left as it is.
func TestJudgeLimitStops(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Running, "")
	w.readyAt = jT0
	now, err := w.b.ServerTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := []lifecycle.Entry{{Boot: "Ba", At: now.Add(-3 * time.Hour).UnixMilli()}, {Boot: "Bb", At: now.Add(-2 * time.Hour).UnixMilli()},
		{Boot: "Bc", At: now.Add(-time.Hour).UnixMilli()}, {Boot: "Bz", At: now.Add(-25 * time.Hour).UnixMilli()}}
	if err := lifecycle.WriteCounter(w.dir, old); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.b.Suspend(context.Background(), bus.SuspendSpec{By: "charlie", Notify: "master", Reason: "the person's"}); err != nil {
		t.Fatal(err)
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	rec, _ := w.record()
	if rec.Verdict != bus.RebootStopped || rec.Why != "limit: 4 in 24 h" || rec.N != 4 || rec.Max != 3 || rec.Window != (24*time.Hour).Milliseconds() {
		t.Fatalf("record %+v", rec)
	}
	if es := w.counter(); len(es) != 4 {
		t.Fatalf("counter %+v (the old entry out of the window dropped, this boot added)", es)
	}
	if p, _ := w.b.Pause(context.Background()); p == nil || p.By != "charlie" {
		t.Fatalf("the person's suspend replaced: %+v", p)
	}
}

// The lease lost: nothing is written, the error is the lease's.
func TestJudgeWithoutTheLease(t *testing.T) {
	w := newJudgeWorld(t)
	w.before(lifecycle.Stopped, lifecycle.CauseSignal)
	if err := w.j.run(context.Background(), "other"); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("err %v", err)
	}
	if _, ok := w.record(); ok || len(w.notices()) != 0 {
		t.Fatal("written without the lease")
	}
	if r := w.runRecord(); r.Boot != "B0" {
		t.Fatalf("run record rewritten: %+v", r)
	}
}

// The dispatch service's own config judges a reboot (reboot-resume §3):
// its judge reads this project's state directory and this host's boot, and
// the machine's settings.
func TestServiceDispatchConfigJudgesTheReboot(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	withBoot(t, "B7")
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	cfg, _, err := serviceDispatchConfig(b, "demo", "", paths, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Reboot == nil {
		t.Fatal("the dispatch service judges no reboot")
	}
	j := serviceRebootJudge(b, "demo", paths, os.Getenv, root, io.Discard)
	if j.dir != paths.StateDir("demo") || j.boot != "B7" || j.escalate() != "master" {
		t.Fatalf("judge %+v", j)
	}
	if s := j.settings(); !s.Restart || s.Max != 3 || s.Window != 24*time.Hour || s.SelfRepair != "on" {
		t.Fatalf("settings %+v", s)
	}
	t.Setenv(config.EnvName("services.unplanned_max"), "5")
	t.Setenv(config.EnvName("services.unplanned_restart"), "0")
	if s := j.settings(); s.Restart || s.Max != 5 {
		t.Fatalf("settings from the environment %+v", s)
	}
	if svc := j.services(); !svc["dispatch"] || !svc["holder"] {
		t.Fatalf("services %v", svc)
	}
}
