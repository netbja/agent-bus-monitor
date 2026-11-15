package dispatch

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// hostFixture is a HostRuntime over the fake host, its clock in the test's
// hands, with one terminal "coder" running Claude Code.
type hostFixture struct {
	h    *fake.Host
	rt   *HostRuntime
	now  time.Time
	term termhost.Term
}

func newHostFixture(t *testing.T) *hostFixture {
	t.Helper()
	f := &hostFixture{h: fake.New(), now: time.UnixMilli(1_800_000_000_000)}
	var err error
	if f.term, err = f.h.Start(context.Background(), termhost.Spec{Project: "demo", Name: "coder"}); err != nil {
		t.Fatal(err)
	}
	f.rt = &HostRuntime{Host: f.h, Project: "demo", Now: func() time.Time { return f.now },
		Client: func(agent string) string {
			if agent == "coder" {
				return "claude"
			}
			return "nope"
		}}
	f.screen(t, "claude-idle.txt")
	f.quiet(time.Second)
	return f
}

// screen shows a captured screen of agentstate's fixtures.
func (f *hostFixture) screen(t *testing.T, name string) {
	t.Helper()
	b, err := os.ReadFile("../agentstate/testdata/screens/" + name)
	if err != nil {
		t.Fatal(err)
	}
	f.h.SetScreen(f.term.ID, strings.Split(string(b), "\n")...)
}

// quiet: the program last wrote d ago.
func (f *hostFixture) quiet(d time.Duration) {
	f.h.SetActivity(f.term.ID, termhost.Activity{OutputAt: f.now.Add(-d)})
}

func (f *hostFixture) inspect() Inspection {
	return f.rt.Inspect(context.Background(), string(f.term.ID))
}

// herdr exit step 9 (spec 4.3, 4.5): on a host, a settled screen (output
// quiet 800 ms) with the client's ready signature is ready and idle; the
// identity is the host's own child, with its start time.
func TestHostRuntimeInspectsAReadyTerminal(t *testing.T) {
	f := newHostFixture(t)
	in := f.inspect()
	if in.Err != nil || in.Gone || !in.Ready || in.Busy || in.State != "idle" || in.Client != "claude" || in.Focused {
		t.Fatalf("ready terminal: %+v", in)
	}
	if len(in.PIDs) != 1 || in.PIDs[0] != f.term.PID || in.Starts[f.term.PID] != f.term.PIDStart {
		t.Fatalf("identity %v %v, want the host's child %d/%d", in.PIDs, in.Starts, f.term.PID, f.term.PIDStart)
	}
	if in.ScreenHash == "" || in.Blocked == nil || in.Blocked.PID != f.term.PID || !in.Blocked.Settled || in.Blocked.Screen {
		t.Fatalf("screen hash %q, blocked look %+v", in.ScreenHash, in.Blocked)
	}
}

// Output within the settle time is not a settled screen: never ready, and
// not a look that counts for the blocked signature.
func TestHostRuntimeNeedsASettledScreen(t *testing.T) {
	f := newHostFixture(t)
	f.quiet(300 * time.Millisecond)
	if in := f.inspect(); in.Ready || in.ScreenHash != "" || in.Blocked.Settled {
		t.Fatalf("unsettled: %+v", in)
	}
}

// A person at the terminal (a draft, or typing within dispatch.person_quiet)
// is what Focused meant under herdr.
func TestHostRuntimePersonActiveIsFocused(t *testing.T) {
	f := newHostFixture(t)
	f.h.SetActivity(f.term.ID, termhost.Activity{OutputAt: f.now.Add(-time.Second), Draft: true})
	if in := f.inspect(); !in.Focused {
		t.Fatalf("a draft: %+v", in)
	}
	f.h.SetActivity(f.term.ID, termhost.Activity{OutputAt: f.now.Add(-time.Second), ViewerTypedAt: f.now.Add(-2 * time.Second)})
	if in := f.inspect(); !in.Focused {
		t.Fatalf("typing 2 s ago: %+v", in)
	}
	f.h.SetActivity(f.term.ID, termhost.Activity{OutputAt: f.now.Add(-time.Second), ViewerTypedAt: f.now.Add(-time.Minute)})
	if in := f.inspect(); in.Focused {
		t.Fatalf("typed a minute ago: %+v", in)
	}
}

// The client's Blocked signature on a settled screen is a blocked look (4.3a)
// naming what it asks; the state is blocked, never ready.
func TestHostRuntimeSeesABlockedMenu(t *testing.T) {
	f := newHostFixture(t)
	f.screen(t, "claude-modal.txt")
	in := f.inspect()
	if in.State != "blocked" || in.Ready || in.Blocked == nil || !in.Blocked.Screen || !strings.Contains(in.Blocked.What, "?") {
		t.Fatalf("blocked menu: state %q ready %v look %+v", in.State, in.Ready, in.Blocked)
	}
}

// A client without a profile is unsupported: inspected, never ready.
func TestHostRuntimeUnsupportedClientIsNeverReady(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Client = func(string) string { return "nope" }
	if in := f.inspect(); in.Ready || in.State != "unknown" || in.Err != nil {
		t.Fatalf("unsupported: %+v", in)
	}
}

// A terminal the host does not know, or whose program ended, is gone.
func TestHostRuntimeGone(t *testing.T) {
	f := newHostFixture(t)
	if in := f.rt.Inspect(context.Background(), "fake-99"); !in.Gone || in.Ready {
		t.Fatalf("unknown terminal: %+v", in)
	}
	if err := f.h.Close(context.Background(), f.term.ID); err != nil {
		t.Fatal(err)
	}
	if in := f.inspect(); !in.Gone || in.Ready {
		t.Fatalf("closed terminal: %+v", in)
	}
}

// Background work (4.3b) from the terminal's process tree: the first look
// has no window yet (unknown, fail closed); a later one judges the
// descendants (the host's child itself is the agent, never its own
// background); an idle helper counts for nothing, an active one runs.
func TestHostRuntimeJudgesBackgroundWork(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Hooks = func(string) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
	p := f.term.PID
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude", CPU: time.Second})
	if in := f.inspect(); in.Background == nil || in.Background.State != agentstate.BackgroundUnknown {
		t.Fatalf("first look: %+v", in.Background)
	}
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude", CPU: 9 * time.Second})
	if in := f.inspect(); in.Background == nil || in.Background.State != agentstate.BackgroundNone {
		t.Fatalf("the agent alone, busy itself: %+v", in.Background)
	}
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
		termhost.Proc{PID: p + 1, PPID: p, Start: 7, Command: "pytest"})
	if in := f.inspect(); in.BackgroundSkip() != "background work: process 'pytest'" {
		t.Fatalf("a child: %q", in.BackgroundSkip())
	}
	if in := f.inspect(); len(in.PIDs) != 2 || in.Starts[p+1] != 7 {
		t.Fatalf("the tree's pids: %v %v", in.PIDs, in.Starts)
	}
	// Without the hooks' facts, Claude Code's background is unknown.
	f.rt.Hooks = nil
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"})
	if in := f.inspect(); in.Background == nil || in.Background.State != agentstate.BackgroundUnknown {
		t.Fatalf("hooks unread: %+v", in.Background)
	}
}

// Prompt and Clear submit a Line through the host and nothing else; the
// host's refusals come back as errors, a line not seen is not observed.
func TestHostRuntimeSubmits(t *testing.T) {
	ctx := context.Background()
	f := newHostFixture(t)
	f.h.SetReady(f.term.ID, true)
	line, err := termhost.WakeLine("demo", "coder", 2, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.rt.Prompt(ctx, string(f.term.ID), line); !ok || err != nil {
		t.Fatalf("prompt: %v %v", ok, err)
	}
	if err := f.rt.Clear(ctx, string(f.term.ID)); err != nil {
		t.Fatal(err)
	}
	if got := f.h.Submitted(f.term.ID); len(got) != 2 || got[0] != line.Text() || got[1] != termhost.ClearLine().Text() {
		t.Fatalf("submitted %q", got)
	}
	if ok, err := f.rt.Prompt(ctx, string(f.term.ID), termhost.ClearLine()); ok || err == nil {
		t.Fatal("prompt typed a /clear")
	}
	f.h.SetPersonActive(f.term.ID, true)
	if ok, err := f.rt.Prompt(ctx, string(f.term.ID), line); ok || !errors.Is(err, termhost.ErrPersonActive) {
		t.Fatalf("person active: %v %v", ok, err)
	}
	f.h.SetPersonActive(f.term.ID, false)
	f.h.SetLands(f.term.ID, false)
	if ok, err := f.rt.Prompt(ctx, string(f.term.ID), line); ok || !errors.Is(err, termhost.ErrNotLanded) {
		t.Fatalf("not landed: %v %v", ok, err)
	}
	if err := f.rt.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
