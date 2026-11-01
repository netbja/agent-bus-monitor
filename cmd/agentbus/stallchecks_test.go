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
