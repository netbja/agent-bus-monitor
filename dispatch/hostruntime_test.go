package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
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
	f.rt.Hooks = func(string, int) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
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
	// A tool command runs under the client's shell (claude → bash → pytest).
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
		termhost.Proc{PID: p + 1, PPID: p, Start: 6, Command: "bash"},
		termhost.Proc{PID: p + 2, PPID: p + 1, Start: 7, Command: "pytest"})
	if in := f.inspect(); in.BackgroundSkip() != "background work: process 'bash', process 'pytest'" {
		t.Fatalf("a command: %q", in.BackgroundSkip())
	}
	// Its age counts from the dispatcher's first sight of it (background-work-visible step 2).
	f.now = f.now.Add(4 * time.Minute)
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
		termhost.Proc{PID: p + 1, PPID: p, Start: 6, Command: "bash"},
		termhost.Proc{PID: p + 2, PPID: p + 1, Start: 7, Command: "pytest", CPU: time.Second})
	// The skip's reason stays the same text as the work goes on: the
	// dispatcher's once-per-change event and clear-deferred line compare it (foureyes).
	if in := f.inspect(); in.Background.Procs() != 2 || in.Background.OldestAge() != 4*time.Minute || in.BackgroundSkip() != "background work: process 'bash', process 'pytest'" {
		t.Fatalf("4 minutes on: %d %v %q", in.Background.Procs(), in.Background.OldestAge(), in.BackgroundSkip())
	}
	if in := f.inspect(); len(in.PIDs) != 3 || in.Starts[p+2] != 7 {
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

// movingHost is a host whose program writes while its screen is read: each
// Activity reports a later output than the one before.
type movingHost struct {
	*fake.Host
	at time.Time
}

func (m *movingHost) Activity(ctx context.Context, id termhost.TermID) (termhost.Activity, error) {
	m.at = m.at.Add(time.Millisecond)
	return termhost.Activity{OutputAt: m.at}, nil
}

// Output during the read: whatever the screen shows, it is not settled.
func TestHostRuntimeOutputDuringTheReadIsNotSettled(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Host = &movingHost{Host: f.h, at: f.now.Add(-time.Hour)}
	if in := f.inspect(); in.Ready || in.Blocked.Settled {
		t.Fatalf("output while reading: %+v", in)
	}
}

// A helper (an MCP server the launcher configured) is no background work
// while idle; one whose CPU grew past agentstate.helper_cpu of the window is.
// Here it is not the client's direct child (one that is, is the client's own
// helper whatever its CPU: TestHostRuntimeIgnoresTheClientsMCPServers).
func TestHostRuntimeHelperCountsOnlyWhenActive(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Hooks = func(string, int) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
	f.rt.ExtraHelpers = func(string) []string { return []string{"mcp-server"} }
	p := f.term.PID
	at := func(cpu time.Duration) Inspection {
		f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
			termhost.Proc{PID: p + 1, PPID: p + 5, Start: 9, Command: "mcp-server", CPU: cpu})
		return f.inspect()
	}
	at(time.Second)
	f.now = f.now.Add(10 * time.Second)
	if in := at(time.Second + 100*time.Millisecond); in.BackgroundSkip() != "" { // 1 % of the window
		t.Fatalf("an idle helper: %q", in.BackgroundSkip())
	}
	f.now = f.now.Add(10 * time.Second)
	if in := at(4 * time.Second); in.BackgroundSkip() != "background work: process 'mcp-server'" { // 29 %
		t.Fatalf("an active helper: %q", in.BackgroundSkip())
	}
}

// A terminal found gone drops its CPU samples (foureyes on herdr-9).
func TestHostRuntimeForgetsAGoneTerminal(t *testing.T) {
	f := newHostFixture(t)
	f.inspect()
	if err := f.h.Close(context.Background(), f.term.ID); err != nil {
		t.Fatal(err)
	}
	f.inspect()
	f.rt.mu.Lock()
	defer f.rt.mu.Unlock()
	if n := len(f.rt.samples); n != 0 {
		t.Fatalf("%d terminals' samples kept", n)
	}
}

// Holder hardening G1: a screen the holder does not vouch for (lagging,
// invalid, stuck) is an inspection error: never ready, never gone, and
// the dispatcher waits (rule 7), so nothing is typed into it.
func TestHostRuntimeUntrustedScreenIsUnknown(t *testing.T) {
	f := newHostFixture(t)
	f.h.SetScreenErr(f.term.ID, fmt.Errorf("%w (terminal t1's screen is stuck)", termhost.ErrScreenUntrusted))
	in := f.inspect()
	if !errors.Is(in.Err, termhost.ErrScreenUntrusted) || in.Ready || in.Gone || in.State != "unknown" {
		t.Fatalf("an untrusted screen: %+v", in)
	}
}

// agent-screen-blocked §3: a settled screen the client's signature does not
// read (neither ready, nor busy, nor a prompt) inspects as unknown with a
// screen hash, the stuck screen's measure; the hash is an opaque digest, so
// no word of the screen reaches the observation (never written to Redis).
func TestHostRuntimeUnrecognisedScreenIsUnknownWithAHash(t *testing.T) {
	f := newHostFixture(t)
	f.h.SetScreen(f.term.ID, "zq-secret-word-7731 something no signature reads", "", "another plain line")
	f.quiet(time.Second)
	in := f.inspect()
	if in.State != "unknown" || in.Ready || in.Busy || in.ScreenHash == "" {
		t.Fatalf("unrecognised screen: state %q ready %v busy %v hash %q", in.State, in.Ready, in.Busy, in.ScreenHash)
	}
	o := bus.Observation{State: in.State, PID: f.term.PID, ScreenHash: in.ScreenHash}
	carryScreen(&o, bus.Observation{}, false)
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "zq-secret-word") || strings.Contains(string(b), "another plain line") {
		t.Fatalf("screen text in the observation: %s", b)
	}
}

// master's first real false positive (2026-11-21): docs's pop-retire was
// skipped for 2 h on "process 'codebase-memory'", Claude Code's own MCP stdio
// server, a direct child of the client (comm cut to 15 characters). The
// client's direct children that are not a shell are its helpers, whatever
// their CPU; a forgotten dev server under the client's shell still blocks.
// Codex (reviewer-codex's tree, 2026-11-21): its code-mode host and
// codebase-memory-mcp are listed helper branches; codegraph's node is
// MainThread like any node, so it still counts, and so does a command bash
// exec'd in place.
func TestHostRuntimeCodexHelperBranches(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Client = func(string) string { return "codex" }
	f.rt.Ignore = []string{} // the profile alone (dispatch.background_ignore lists MainThread by default)
	p := f.term.PID
	tree := func(cpu time.Duration, more ...termhost.Proc) []termhost.Proc {
		return append([]termhost.Proc{{PID: p, Start: f.term.PIDStart, Command: "codex"},
			{PID: p + 1, PPID: p, Start: 5, Command: "codex-code-mode", CPU: cpu},
			{PID: p + 2, PPID: p, Start: 5, Command: "codebase-memory", CPU: cpu}}, more...)
	}
	f.h.SetProcesses(f.term.ID, tree(time.Second)...)
	f.inspect()
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, tree(9*time.Second)...)
	if in := f.inspect(); in.Background == nil || in.Background.Procs() != 0 || in.Background.What() != "" {
		t.Fatalf("codex's own helpers, busy: %+v", in.Background)
	}
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, tree(9*time.Second,
		termhost.Proc{PID: p + 3, PPID: p, Start: 8, Command: "MainThread"},
		termhost.Proc{PID: p + 4, PPID: p, Start: 8, Command: "npm"})...)
	if in := f.inspect(); in.Background.What() != "process 'MainThread', process 'npm'" {
		t.Fatalf("node and a command exec'd in place: %q", in.Background.What())
	}
}

func TestHostRuntimeIgnoresTheClientsMCPServers(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Hooks = func(string, int) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
	p := f.term.PID
	mcp := func(cpu time.Duration) []termhost.Proc {
		return []termhost.Proc{{PID: p, Start: f.term.PIDStart, Command: "claude"},
			{PID: p + 1, PPID: p, Start: 5, Command: "codebase-memory", CPU: cpu}, // codebase-memory-mcp, as /proc names it
			{PID: p + 2, PPID: p, Start: 5, Command: "npm exec", CPU: cpu},        // an npx-started one (context7)…
			{PID: p + 6, PPID: p + 2, Start: 5, Command: "node", CPU: cpu},        // …whose server is its child
			{PID: p + 7, PPID: p, Start: 5, Command: "MainThread", CPU: cpu},      // codegraph's node, as /proc names it…
			{PID: p + 8, PPID: p + 7, Start: 5, Command: "MainThread", CPU: cpu}}  // …and its watcher
	}
	f.h.SetProcesses(f.term.ID, mcp(time.Second)...)
	f.inspect()
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, mcp(9*time.Second)...) // busy: reindexing
	if in := f.inspect(); in.Background == nil || in.Background.State != agentstate.BackgroundNone || in.BackgroundSkip() != "" {
		t.Fatalf("the client's MCP servers, busy: %+v %q", in.Background, in.BackgroundSkip())
	}
	// A dev server the agent left running: claude → bash → npm → node.
	f.now = f.now.Add(10 * time.Second)
	f.h.SetProcesses(f.term.ID, append(mcp(9*time.Second),
		termhost.Proc{PID: p + 3, PPID: p, Start: 8, Command: "bash"},
		termhost.Proc{PID: p + 4, PPID: p + 3, Start: 8, Command: "npm"},
		termhost.Proc{PID: p + 5, PPID: p + 4, Start: 8, Command: "node"})...)
	if in := f.inspect(); in.BackgroundSkip() != "background work: process 'bash', process 'npm', process 'node'" || in.Background.Procs() != 3 {
		t.Fatalf("a forgotten dev server: %q (%d)", in.BackgroundSkip(), in.Background.Procs())
	}
}
