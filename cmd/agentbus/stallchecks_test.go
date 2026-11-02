package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// stallWorld: a project with master and coder idle, their panes seen idle,
// and a plan whose task t1 is ready for coder. The reader's clock runs 20
// minutes ahead, so every declaration is past the 15-minute threshold.
type stallWorld struct {
	t   *testing.T
	b   *bus.Bus
	s   *stallReader
	env map[string]string
}

func newStallWorld(t *testing.T) *stallWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "stall"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	who := bus.AgentIdent{PID: os.Getpid(), Provider: "anthropic"}
	for _, a := range []string{"master", "coder"} {
		if _, err := b.Status(ctx, a, "idle", "", who); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(20 * time.Minute)
	if ok, err := b.AcquireDispatch(ctx, "stall-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	seen := bus.Observation{State: "idle", At: later.UnixMilli()}
	if err := b.FencedPutObserved(ctx, "stall-holder", map[string]bus.Observation{"master": seen, "coder": seen}); err != nil {
		t.Fatal(err)
	}
	file := []byte(`{"plan":"t","goal":"g","tasks":[{"id":"t1","title":"Next task","owner":"coder"}]}`)
	if err := b.LoadPlan(ctx, "docs/plans/t.json", "0123456789abcdef0123456789abcdef01234567", file, []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	w := &stallWorld{t: t, b: b, env: map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()}}
	master := "master"
	w.s = newStallReader(b, func(k string) string { return w.env[k] }, dispatch.ClearConfig{On: true, Decisions: dispatch.NewClearDecisions()}, &master)
	w.s.now = func() time.Time { return later }
	return w
}

func (w *stallWorld) run(check func(context.Context) (health.Outcome, error)) health.Outcome {
	w.t.Helper()
	out, err := check(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

// A team idle past the threshold, nothing open and a task ready: the fail
// that wakes sentinel; a request opened makes it ok, naming the request.
func TestTeamStallReadsALiveProject(t *testing.T) {
	w := newStallWorld(t)
	w.env["AGENT_BUS_QUIET_AFTER"] = "0" // judged without quiet-agent's record (QuietOff)
	out := w.run(w.s.teamStall)
	if out.Status != health.Fail || out.Reason != "stalled" || !strings.Contains(out.Detail, "next ready: t1 (coder)") {
		t.Fatalf("stalled team: %+v", out)
	}
	if _, err := w.b.Request(context.Background(), "t1", "master", "coder", "", "do t1", 0); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.teamStall); out.Status != health.OK || !strings.Contains(out.Detail, "work is open: 1 request") {
		t.Fatalf("with a request: %+v", out)
	}
	w.env["AGENT_BUS_STALL_AFTER"] = "0"
	if out := w.run(w.s.teamStall); out.Status != health.Unknown || out.Detail != "off (stall_after = 0)" {
		t.Fatalf("off: %+v", out)
	}
}

// Without quiet-agent's record (quiet_after on), team-stall cannot judge.
func TestTeamStallNeedsTheQuietRecord(t *testing.T) {
	w := newStallWorld(t)
	if out := w.run(w.s.teamStall); out.Status != health.Unknown || out.Detail != "quiet-agent not measured" {
		t.Fatalf("no quiet record: %+v", out)
	}
}

// A directive drained by coder, then silence: quiet; a report since: not.
func TestQuietAgentReadsALiveProject(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	time.Sleep(2 * time.Millisecond) // the directive in a later millisecond than coder's status
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "do the next task"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.quietAgent); out.Status != health.Warn || !strings.Contains(out.Detail, "coder: directive from master") {
		t.Fatalf("drained and silent: %+v", out)
	}
	if _, err := w.b.Report(ctx, "coder", bus.ReportNote, "on it"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.quietAgent); out.Status != health.OK {
		t.Fatalf("after a report: %+v", out)
	}
}

// putStall stores a team-stall fail, as the health loop does.
func (w *stallWorld) putStall() health.Record {
	w.t.Helper()
	ctx := context.Background()
	if err := w.b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		w.t.Fatal(err)
	}
	rec := health.Record{ID: "team-stall", Status: health.Fail, Detail: "team idle 18m, nothing open; next ready: t1 (coder)", Fingerprint: "f"}
	if err := w.b.FencedHealthPut(ctx, "stall-holder", "master", rec); err != nil {
		w.t.Fatal(err)
	}
	recs, err := w.b.Health(ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return recs["team-stall"]
}

func (w *stallWorld) personNotices() []string {
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

// Master held: the person is told at once, once per episode.
func TestStallNoticeWhenMasterCannotAct(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	rec := w.putStall()
	w.s.now = func() time.Time { return time.UnixMilli(rec.Since).Add(time.Minute) }
	if err := w.b.FencedQuotaSet(ctx, "stall-holder", "hold:anthropic", "1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallNotify(ctx, "stall-holder", rec); err != nil {
			t.Fatal(err)
		}
	}
	got := w.personNotices()
	if len(got) != 1 || !strings.Contains(got[0], "master cannot act (master is held by its provider's limit)") {
		t.Fatalf("notices %q", got)
	}
}

// Master can act: nothing before twice the threshold, one notice after it,
// and the mark goes once the fail ends.
func TestStallNoticeSecondTier(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	rec := w.putStall()
	w.s.now = func() time.Time { return time.UnixMilli(rec.Since).Add(29 * time.Minute) }
	if err := w.s.stallNotify(ctx, "stall-holder", rec); err != nil || len(w.personNotices()) != 0 {
		t.Fatalf("before 2xN: err %v, notices %q", err, w.personNotices())
	}
	w.s.now = func() time.Time { return time.UnixMilli(rec.Since).Add(30 * time.Minute) }
	if err := w.s.stallNotify(ctx, "stall-holder", rec); err != nil {
		t.Fatal(err)
	}
	if got := w.personNotices(); len(got) != 1 || !strings.Contains(got[0], "master was told and nothing moved") {
		t.Fatalf("at 2xN: %q", got)
	}
	ok := health.Record{ID: "team-stall", Status: health.OK, Detail: "coder working", Fingerprint: "g"}
	if err := w.b.FencedHealthPut(ctx, "stall-holder", "master", ok); err != nil {
		t.Fatal(err)
	}
	if err := w.s.stallNotify(ctx, "stall-holder", ok); err != nil {
		t.Fatal(err)
	}
	if m, _ := w.b.QuotaMarks(ctx); m["stall"] != "" {
		t.Fatalf("the mark outlived its episode: %q", m["stall"])
	}
}

// A lost lease writes nothing.
func TestStallNoticeWithoutTheLease(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	rec := w.putStall()
	w.s.now = func() time.Time { return time.UnixMilli(rec.Since).Add(time.Hour) }
	if err := w.s.stallNotify(ctx, "not-the-holder", rec); err == nil || len(w.personNotices()) != 0 {
		t.Fatalf("err %v, notices %q", err, w.personNotices())
	}
}

// A failed read in the middle of a stall (fail, unknown, fail) is still one
// episode: the person is told once, and the mark survives the unknown
// (reviewer-codex).
func TestStallNoticeSurvivesAnUnknownPass(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if err := w.b.FencedQuotaSet(ctx, "stall-holder", "hold:anthropic", "1"); err != nil {
		t.Fatal(err)
	}
	rec := w.putStall()
	w.s.now = func() time.Time { return time.UnixMilli(rec.Since).Add(time.Minute) }
	if err := w.s.stallNotify(ctx, "stall-holder", rec); err != nil {
		t.Fatal(err)
	}
	unknown := health.Record{ID: "team-stall", Status: health.Unknown, Detail: "agents unavailable", Fingerprint: "u"}
	if err := w.b.FencedHealthPut(ctx, "stall-holder", "master", unknown); err != nil {
		t.Fatal(err)
	}
	if err := w.s.stallNotify(ctx, "stall-holder", unknown); err != nil {
		t.Fatal(err)
	}
	if m, _ := w.b.QuotaMarks(ctx); m["stall"] == "" {
		t.Fatal("an unknown pass ended the episode's mark")
	}
	time.Sleep(2 * time.Millisecond)
	again := w.putStall() // a new since, the same stall
	w.s.now = func() time.Time { return time.UnixMilli(again.Since).Add(time.Minute) }
	if err := w.s.stallNotify(ctx, "stall-holder", again); err != nil {
		t.Fatal(err)
	}
	if got := w.personNotices(); len(got) != 1 {
		t.Fatalf("%d notices across fail, unknown, fail: %q", len(got), got)
	}
}

// The second-tier clock is the episode's first since: an unknown blip (a new
// since on the next fail) does not restart it (foureyes, step 3).
func TestStallSecondTierClockSurvivesAnUnknownBlip(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	first := w.putStall()
	w.s.now = func() time.Time { return time.UnixMilli(first.Since).Add(5 * time.Minute) }
	if err := w.s.stallNotify(ctx, "stall-holder", first); err != nil {
		t.Fatal(err)
	}
	unknown := health.Record{ID: "team-stall", Status: health.Unknown, Detail: "agents unavailable", Fingerprint: "u"}
	if err := w.b.FencedHealthPut(ctx, "stall-holder", "master", unknown); err != nil {
		t.Fatal(err)
	}
	if err := w.s.stallNotify(ctx, "stall-holder", unknown); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	again := w.putStall()                                                                   // a new since after the blip
	w.s.now = func() time.Time { return time.UnixMilli(first.Since).Add(30 * time.Minute) } // 2x from the first since
	if err := w.s.stallNotify(ctx, "stall-holder", again); err != nil {
		t.Fatal(err)
	}
	if got := w.personNotices(); len(got) != 1 || !strings.Contains(got[0], "nothing moved") {
		t.Fatalf("the clock restarted after the blip: %q", got)
	}
}

// A woken hooked agent whose turn flipped working then idle, and that wrote
// nothing, is still quiet: a flip is not activity (auto-state, foureyes).
func TestQuietAgentIgnoresTurnFlips(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	time.Sleep(2 * time.Millisecond)
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "do the next task"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	who := bus.AgentIdent{PID: os.Getpid()}
	time.Sleep(2 * time.Millisecond)
	for _, st := range []string{"working", "idle"} {
		if err := w.b.TurnState(ctx, "coder", st, who); err != nil {
			t.Fatal(err)
		}
	}
	if out := w.run(w.s.quietAgent); out.Status != health.Warn || !strings.Contains(out.Detail, "coder: directive from master") {
		t.Fatalf("flips counted as activity: %+v", out)
	}
}

// A turn's working left stuck (no Stop), its pane seen ready for long: team-
// stall reads it idle through the effective state (auto-state step 2).
func TestTeamStallReadsAStuckFlipAsIdle(t *testing.T) {
	w := newStallWorld(t)
	w.env["AGENT_BUS_QUIET_AFTER"] = "0"
	ctx := context.Background()
	if err := w.b.TurnState(ctx, "coder", "working", bus.AgentIdent{PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	later := w.s.now()
	seen := bus.Observation{State: "idle", At: later.UnixMilli(), Since: time.Now().Add(time.Second).UnixMilli()}
	if err := w.b.FencedPutObserved(ctx, "stall-holder", map[string]bus.Observation{"master": seen, "coder": seen}); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.teamStall); out.Status != health.Fail || out.Reason != "stalled" {
		t.Fatalf("a stuck flip blinds team-stall: %+v", out)
	}
	w.env["AGENT_BUS_TURN_STALE_AFTER"] = "0" // off: the flip's working stands
	if out := w.run(w.s.teamStall); out.Status == health.Fail {
		t.Fatalf("turn_stale_after = 0 still reads it idle: %+v", out)
	}
}

// dispatchDirectives is every waking directive from the dispatcher to target.
func (w *stallWorld) dispatchDirectives(target string) []string {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 200)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == "dispatch" && e.Target == target && e.Type == bus.CmdDirective && !e.NoWake {
			out = append(out, e.Message)
		}
	}
	return out
}

// A quiet agent wakes master once per directive it went quiet on; a stalled
// hand-off the agent can fix wakes the agent, once (chain-reliability).
func TestStallWakeSendsOnce(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	time.Sleep(2 * time.Millisecond)
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "do the next task"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.DrainCmd(ctx, "coder", "test", bus.DefaultHorizon, func(bus.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.quietAgent); out.Status != health.Warn {
		t.Fatalf("quiet-agent: %+v", out)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "quiet-agent"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 || !strings.Contains(got[0], "coder read directive") {
		t.Fatalf("master woken %q", got)
	}

	at := time.Now().Add(-30 * time.Minute)
	w.s.mu.Lock()
	w.s.lastClear = &health.ClearStallInput{Now: time.Now(), After: 15 * time.Minute, AutoClear: true,
		Handoffs: []health.ClearHandoff{{Agent: "coder", At: at, Decision: health.NotIdleReason, Report: "1793-0"}}}
	w.s.mu.Unlock()
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "clear-stall"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.dispatchDirectives("coder"); len(got) != 1 || !strings.Contains(got[0], "agentbus handoff coder --report 1793-0") {
		t.Fatalf("coder woken %q", got)
	}
	if err := w.s.stallWake(ctx, "not-the-holder", "quiet-agent"); err == nil {
		t.Fatal("a dispatcher without the lease woke someone")
	}
}
