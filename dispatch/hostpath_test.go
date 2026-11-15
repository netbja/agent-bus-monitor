package dispatch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

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
	if b, u := w.events(bus.EventBlocked), w.events(bus.EventUnblocked); len(b) != 1 || len(u) != 1 || b[0].Detail != enter.Message {
		t.Fatalf("events blocked %+v unblocked %+v", b, u)
	}
}
