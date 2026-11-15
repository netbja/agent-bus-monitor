package dispatch

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// hostWorld is the dispatcher on a terminal host (herdr exit step 9): coder,
// a Claude Code agent in a terminal of the fake host, its status verified
// against the host's child; the Run under test uses HostRuntime.
type hostWorld struct {
	t    *testing.T
	b    *bus.Bus
	h    *fake.Host
	rt   *HostRuntime
	term termhost.Term
}

func newHostWorld(t *testing.T) *hostWorld {
	t.Helper()
	b, _ := dialRun(t)
	w := &hostWorld{t: t, b: b, h: fake.New()}
	var err error
	if w.term, err = w.h.Start(context.Background(), termhost.Spec{Project: b.Project(), Name: "coder"}); err != nil {
		t.Fatal(err)
	}
	w.rt = &HostRuntime{Host: w.h, Project: b.Project(), Client: func(string) string { return "claude" }}
	w.show("claude-idle.txt")
	w.h.SetActivity(w.term.ID, termhost.Activity{OutputAt: time.Now().Add(-time.Hour)})
	w.h.SetReady(w.term.ID, true)
	if _, err := b.Status(context.Background(), "coder", "idle", "",
		bus.AgentIdent{Pane: string(w.term.ID), PID: w.term.PID, PIDStart: w.term.PIDStart}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *hostWorld) show(name string) {
	w.t.Helper()
	b, err := os.ReadFile("../agentstate/testdata/screens/" + name)
	if err != nil {
		w.t.Fatal(err)
	}
	w.h.SetScreen(w.term.ID, strings.Split(string(b), "\n")...)
}

// events are the project's events of kind, oldest first.
func (w *hostWorld) events(kind string) []bus.TermEvent {
	w.t.Helper()
	all, err := w.b.RecentEvents(context.Background(), 100)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []bus.TermEvent
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Kind == kind {
			out = append(out, all[i])
		}
	}
	return out
}

// cmdsTo are the cmd entries addressed to target, oldest first.
func (w *hostWorld) cmdsTo(target string) []bus.Event {
	w.t.Helper()
	evs, _, err := w.b.Recent(context.Background(), []string{"cmd"}, 200)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []bus.Event
	for _, e := range evs {
		if e.Target == target {
			out = append(out, e)
		}
	}
	return out
}

// The wake on a host: the line goes through Submit (bracketed, landed, then
// CR, all the host's), the record says prompted, and {p}:events has it.
func TestHostPathWakesAnIdleAgent(t *testing.T) {
	w := newHostWorld(t)
	send(t, w.b, "coder", "build it")
	start(t, w.b, w.rt, fastConfig())
	waitFor(t, 3*time.Second, "a submitted wake line", func() bool { return len(w.h.Submitted(w.term.ID)) == 1 })
	if got := w.h.Submitted(w.term.ID)[0]; !wakeLinePattern.MatchString(got) || !strings.HasSuffix(got, "subscribe --drain coder") {
		t.Fatalf("submitted %q", got)
	}
	waitFor(t, 2*time.Second, "prompted record and wake event", func() bool {
		r, ok := wakeOf(t, w.b, "coder")
		return ok && r.State == "prompted" && len(w.events(bus.EventWakeTyped)) == 1
	})
}

// A person at the terminal (a draft) defers the wake, as focus did: nothing
// is submitted.
func TestHostPathPersonActiveTypesNothing(t *testing.T) {
	w := newHostWorld(t)
	w.h.SetActivity(w.term.ID, termhost.Activity{OutputAt: time.Now().Add(-time.Hour), Draft: true, DraftSince: time.Now()})
	send(t, w.b, "coder", "build it")
	start(t, w.b, w.rt, fastConfig())
	waitFor(t, 3*time.Second, "a deferred record", func() bool {
		r, ok := wakeOf(t, w.b, "coder")
		return ok && r.State == "deferred"
	})
	if got := w.h.Submitted(w.term.ID); len(got) != 0 {
		t.Fatalf("submitted %q with a person at the terminal", got)
	}
}

// A line not seen in the input (spec 4.2): no CR, the person told that it
// may be left there, the event recorded; the terminal takes no other line.
func TestHostPathNotLandedTellsThePerson(t *testing.T) {
	w := newHostWorld(t)
	w.h.SetLands(w.term.ID, false)
	send(t, w.b, "coder", "build it")
	cfg := fastConfig()
	cfg.Retry = 50 * time.Millisecond
	start(t, w.b, w.rt, cfg)
	waitFor(t, 3*time.Second, "the not-landed notice", func() bool {
		ns, _, _ := w.b.Recent(context.Background(), []string{"notify"}, 20)
		for _, n := range ns {
			if n.Message == NotLandedText("coder", "wake") && n.For == bus.ForPerson {
				return len(w.events(bus.EventWakeNotLanded)) == 1
			}
		}
		return false
	})
	time.Sleep(300 * time.Millisecond) // retries: the left-behind terminal refuses each
	if got := w.h.Submitted(w.term.ID); len(got) != 0 {
		t.Fatalf("submitted %q", got)
	}
	if n := len(w.events(bus.EventWakeNotLanded)); n != 1 {
		t.Fatalf("%d not-landed events, want 1", n)
	}
	if len(w.events(bus.EventWakeRefused)) == 0 {
		t.Fatal("the retries' refusals are not recorded")
	}
}

// herdr exit 4.3a: the Blocked signature on two settled looks sends master
// one waking report naming the menu; its end, one report that wakes nobody;
// each with its event, once per episode.
func TestHostPathReportsBlockedToMaster(t *testing.T) {
	w := newHostWorld(t)
	w.show("claude-modal.txt")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	start(t, w.b, w.rt, cfg)
	waitFor(t, 3*time.Second, "the blocked report", func() bool { return len(w.cmdsTo("master")) == 1 })
	enter := w.cmdsTo("master")[0]
	if enter.From != "dispatch" || !strings.HasPrefix(enter.Message, "blocked: coder (claude) since ") ||
		!strings.Contains(enter.Message, "screen:") || enter.NoWake {
		t.Fatalf("enter %+v", enter)
	}
	time.Sleep(200 * time.Millisecond) // more looks, still blocked: nothing more
	if n := len(w.cmdsTo("master")); n != 1 {
		t.Fatalf("%d reports while blocked, want 1", n)
	}
	w.show("claude-idle.txt")
	waitFor(t, 3*time.Second, "the unblocked report", func() bool { return len(w.cmdsTo("master")) == 2 })
	leave := w.cmdsTo("master")[1]
	if !strings.HasPrefix(leave.Message, "unblocked: coder after ") || !leave.NoWake {
		t.Fatalf("leave %+v", leave)
	}
	waitFor(t, 2*time.Second, "the unblocked event", func() bool { return len(w.events(bus.EventUnblocked)) == 1 })
	marks, err := w.b.QuotaMarks(context.Background())
	if err != nil || marks[blockedMark("coder")] == "" || marks[unblockedMark("coder")] != marks[blockedMark("coder")] {
		t.Fatalf("the leave's mark names another episode than its enter: %v %v", marks, err)
	}
	if b := w.events(bus.EventBlocked); len(b) != 1 || b[0].Detail != enter.Message {
		t.Fatalf("blocked events %+v", b)
	}
}

// The escalation target blocked itself: only the person can answer, so both
// reports are notices for the person, never a cmd to itself.
func TestHostPathBlockedTargetTellsThePerson(t *testing.T) {
	w := newHostWorld(t)
	w.show("claude-modal.txt")
	cfg := fastConfig()
	cfg.Observe, cfg.EscalateTo = 30*time.Millisecond, "coder"
	start(t, w.b, w.rt, cfg)
	waitFor(t, 3*time.Second, "the person's blocked notice", func() bool {
		ns, _, _ := w.b.Recent(context.Background(), []string{"notify"}, 20)
		for _, n := range ns {
			if strings.HasPrefix(n.Message, "blocked: coder (claude) since ") && n.For == bus.ForPerson {
				return true
			}
		}
		return false
	})
	if c := w.cmdsTo("coder"); len(c) != 0 {
		t.Fatalf("cmds to the blocked target itself: %+v", c)
	}
}

// A terminal that ends while blocked ends the episode: the leave is sent.
func TestHostPathGoneTerminalEndsTheEpisode(t *testing.T) {
	w := newHostWorld(t)
	w.show("claude-modal.txt")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	start(t, w.b, w.rt, cfg)
	waitFor(t, 3*time.Second, "the blocked report", func() bool { return len(w.cmdsTo("master")) == 1 })
	if err := w.h.Close(context.Background(), w.term.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "the unblocked report", func() bool {
		c := w.cmdsTo("master")
		return len(c) == 2 && strings.HasPrefix(c[1].Message, "unblocked: coder after ")
	})
}

// herdr's Runtime (the running dispatcher until the cutover) writes no
// {p}:events: a wake typed through it changes nothing of before.
func TestHerdrPathWritesNoEvents(t *testing.T) {
	b, _ := dialRun(t)
	register(t, b, "coder", "p-coder")
	send(t, b, "coder", "build it")
	rt := newFakeRT(alwaysReady)
	start(t, b, rt, fastConfig())
	waitFor(t, 3*time.Second, "a prompt", func() bool { return len(rt.Prompts()) == 1 })
	time.Sleep(100 * time.Millisecond)
	if evs, err := b.RecentEvents(context.Background(), 10); err != nil || len(evs) != 0 {
		t.Fatalf("events on herdr's path: %+v %v", evs, err)
	}
}

// reviewer-codex on herdr-9: a viewer attached and not proven unfocused is a
// person at the terminal (spec 4.5). The wake waits there as under herdr's
// focus rule (rule 8, "pane focused"): nothing reserved, nothing submitted,
// no refusal turned into tries and an escalation.
func TestHostPathAttachedViewerDefersTheWake(t *testing.T) {
	w := newHostWorld(t)
	w.h.SetActivity(w.term.ID, termhost.Activity{OutputAt: time.Now().Add(-time.Hour), FocusedViewers: 1})
	send(t, w.b, "coder", "build it")
	start(t, w.b, w.rt, fastConfig())
	waitFor(t, 3*time.Second, "a deferred record", func() bool {
		r, ok := wakeOf(t, w.b, "coder")
		return ok && r.State == "deferred" && r.Reason == "pane focused"
	})
	time.Sleep(300 * time.Millisecond)
	r, _ := wakeOf(t, w.b, "coder")
	if got := w.h.Submitted(w.term.ID); len(got) != 0 || r.Tries != 0 || r.Escalated != "" {
		t.Fatalf("submitted %q, record %+v", got, r)
	}
	if n := len(w.events(bus.EventWakeRefused)); n != 0 {
		t.Fatalf("%d refusals: the wake was attempted", n)
	}
	if c := w.cmdsTo("master"); len(c) != 0 {
		t.Fatalf("escalated: %+v", c)
	}
}

// foureyes on herdr-9: a blocked screen in a terminal whose process is not
// the agent's verified one (another program, a reused pid) is no fact about
// the agent: master is told nothing.
func TestHostPathBlockedScreenOfAnotherProcessIsNotReported(t *testing.T) {
	w := newHostWorld(t)
	if _, err := w.b.Status(context.Background(), "coder", "idle", "",
		bus.AgentIdent{Pane: string(w.term.ID), PID: w.term.PID, PIDStart: w.term.PIDStart + 1}); err != nil {
		t.Fatal(err)
	}
	w.show("claude-modal.txt")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	start(t, w.b, w.rt, cfg)
	time.Sleep(500 * time.Millisecond)
	if c := w.cmdsTo("master"); len(c) != 0 {
		t.Fatalf("reported another process's screen: %+v", c)
	}
}

// foureyes on herdr-9: a report that fails to be written does not move the
// episode on, so the next look sends it.
func TestBlockedReportFailureIsRetried(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	l := &loop{b: b, token: "holder", last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard}}
	look := func(at time.Time) error {
		return l.blockedLook(ctx, "coder", "claude", agentstate.Observation{PID: 7, At: at, Settled: true, Screen: true, What: "Pick?"})
	}
	t0 := time.UnixMilli(1_800_000_000_000)
	if err := look(t0); err != nil { // the first settled look: no report yet
		t.Fatal(err)
	}
	if err := look(t0.Add(time.Second)); !errors.Is(err, bus.ErrNotHolder) { // the report fails: no lease
		t.Fatalf("report without the lease: %v", err)
	}
	if ok, err := b.AcquireDispatch(ctx, "holder", time.Minute); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if err := look(t0.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	evs, _, err := b.Recent(ctx, []string{"cmd"}, 10)
	if err != nil || len(evs) != 1 || evs[0].Target != "master" || !strings.HasPrefix(evs[0].Message, "blocked: coder (claude) since ") {
		t.Fatalf("the report after the failure: %+v %v", evs, err)
	}
}
