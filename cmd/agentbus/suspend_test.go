package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
)

// suspendWorld is one disposable project, a fake service manager with both
// services installed and active, and fake panes.
type suspendWorld struct {
	t       *testing.T
	b       *bus.Bus
	mgr     *fakeManager
	state   string // the state directory
	mu      sync.Mutex
	panes   map[string]dispatch.Inspection // pane -> what an inspection sees
	onRound func(n int)                    // called before each round's reading
	rounds  int
	sigs    chan os.Signal
	env     map[string]string
}

func newSuspendWorld(t *testing.T) *suspendWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "susp"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	w := &suspendWorld{t: t, b: b, mgr: newFakeManager(), state: t.TempDir(), panes: map[string]dispatch.Inspection{},
		sigs: make(chan os.Signal, 2), env: map[string]string{"HOME": t.TempDir()}}
	for _, k := range service.Kinds {
		w.mgr.specs[b.Project()+"/"+k] = service.Spec{Project: b.Project(), Kind: k}
		w.mgr.active[b.Project()+"/"+k] = true
	}
	return w
}

// agent registers a verified agent of this process with its pane seen ready.
func (w *suspendWorld) agent(name, state string) {
	w.t.Helper()
	who := selfIdent("p-" + name)
	if _, err := w.b.Status(context.Background(), name, state, "", who); err != nil {
		w.t.Fatal(err)
	}
	w.setPane(name, dispatch.Inspection{State: "idle", Ready: true})
}

// selfIdent is this test process as an agent's verified identity, start
// included, so that dispatch.ProcGone reads it alive.
func selfIdent(pane string) bus.AgentIdent {
	start, _ := dispatch.ProcStart(os.Getpid())
	return bus.AgentIdent{PID: os.Getpid(), PIDStart: start, Pane: pane, Provider: "anthropic"}
}

func (w *suspendWorld) setPane(name string, in dispatch.Inspection) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.panes["p-"+name] = in
}

func (w *suspendWorld) deps() suspendDeps {
	return suspendDeps{
		Getenv:   func(k string) string { return w.env[k] },
		Bus:      func(string) (*bus.Bus, error) { return w.b, nil },
		Projects: func(context.Context) ([]string, error) { return []string{w.b.Project()}, nil },
		Services: func() (service.Manager, string) { return w.mgr, "" },
		Inspect: func(_ context.Context, pane string) dispatch.Inspection {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.panes[pane]
		},
		ProcStart: dispatch.ProcStart,
		Lock:      func(string) (func(), error) { return func() {}, nil },
		Hostname:  func() (string, error) { return "testhost", nil },
		Boot:      func() string { return "boot-1" },
		StateDir:  func(string) string { return w.state },
		Person:    func() string { return "charlie" },
		Round:     5 * time.Millisecond,
		Settle:    20 * time.Millisecond,
		BeforeRound: func(n int) {
			w.rounds = n
			if w.onRound != nil {
				w.onRound(n)
			}
		},
		Signals:     func() (<-chan os.Signal, func()) { return w.sigs, func() {} },
		AgentCaller: func(context.Context) (string, error) { return "", nil },
	}
}

func (w *suspendWorld) run(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runSuspend(context.Background(), append([]string{"--yes"}, args...), w.b.Project(), w.deps(), &out, &errw)
	return code, out.String(), errw.String()
}

func (w *suspendWorld) pause() *bus.PauseRecord {
	p, err := w.b.Pause(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func TestSuspendQuietProjectWholeOrder(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("master", "idle")
	w.agent("coder", "idle")
	code, out, errw := w.run("--reason", "OS updates")
	if code != 0 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	p := w.pause()
	if p == nil || !p.Suspended() || !p.Services["dispatch"] || !p.Services["missions"] || p.Boot != "boot-1" ||
		p.Host != "testhost" || p.By != "charlie" || p.Reason != "OS updates" {
		t.Fatalf("pause = %+v", p)
	}
	// dispatch first (right after the pause), then missions at the end.
	want := []string{"stop " + w.b.Project() + " dispatch", "stop " + w.b.Project() + " missions"}
	if got := w.mgr.calls; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("manager calls = %q, want %q", got, want)
	}
	snap := readSnapshot(t, w.state)
	if snap.V != 1 || snap.Project != w.b.Project() || !snap.Services["dispatch"].WasActive || !snap.Services["missions"].Stopped ||
		len(snap.Agents) != 2 || snap.Agents["coder"].State != "idle" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if fi, err := os.Stat(filepath.Join(w.state, "suspend.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode %v %v", fi, err)
	}
	if !strings.Contains(out, "suspended") {
		t.Fatalf("out = %q", out)
	}
}

func TestSuspendWaitsForAWorkingAgent(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	w.setPane("coder", dispatch.Inspection{State: "working", Busy: true})
	w.onRound = func(n int) {
		if n == 3 { // the turn ends: the agent declares idle
			if _, err := w.b.Status(context.Background(), "coder", "idle", "", selfIdent("p-coder")); err != nil {
				t.Error(err)
			}
			w.setPane("coder", dispatch.Inspection{State: "idle", Ready: true})
		}
	}
	code, out, errw := w.run("--wait", "5s")
	if code != 0 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	if w.rounds < 4 {
		t.Fatalf("rounds = %d: suspend did not wait for the turn", w.rounds)
	}
	if !strings.Contains(out, "coder: working") {
		t.Fatalf("the busy agent was not said: %q", out)
	}
}

func TestSuspendTimeoutCancelsAndRestartsDispatch(t *testing.T) {
	w := newSuspendWorld(t)
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	if _, err := w.b.SetPause(ctx, "dana", "master", "lunch", until); err != nil {
		t.Fatal(err)
	}
	w.agent("coder", "working")
	w.agent("master", "idle")
	code, out, errw := w.run("--wait", "50ms")
	if code != 1 {
		t.Fatalf("suspend = %d, want 1 (cancelled)\n%s\n%s", code, out, errw)
	}
	p := w.pause()
	if p == nil || p.Suspended() || p.Reason != "lunch" || p.Until != until.UnixMilli() {
		t.Fatalf("pause after the cancel = %+v, want the previous timed pause back", p)
	}
	if !w.mgr.active[w.b.Project()+"/dispatch"] || !w.mgr.active[w.b.Project()+"/missions"] {
		t.Fatalf("services after the cancel: %v", w.mgr.active)
	}
	if !strings.Contains(errw, "coder") {
		t.Fatalf("the busy agent was not named: %q", errw)
	}
	if _, err := os.Stat(filepath.Join(w.state, "suspend.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a snapshot was written for a cancelled suspend: %v", err)
	}
}

// With no previous pause, the cancel lifts it and the dispatcher, restarted,
// wakes master for the resume directive.
func TestSuspendCancelSendsTheResumeDirective(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	w.agent("master", "idle")
	if code, _, _ := w.run("--wait", "50ms"); code != 1 {
		t.Fatalf("suspend = %d, want 1", code)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("still paused: %+v", p)
	}
	evs, _, _ := w.b.Recent(context.Background(), []string{"cmd"}, 10)
	last := evs[len(evs)-1]
	if last.Target != "master" || last.NoWake || !last.Human || !strings.Contains(last.Message, "cancelled") {
		t.Fatalf("cancel directive = %+v", last)
	}
}

func TestSuspendFailedRestartKeepsTheSuspend(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	w.mgr.failStart = true
	code, out, errw := w.run("--wait", "50ms")
	if code != 1 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	p := w.pause()
	if p == nil || !p.Suspended() || !p.Services["dispatch"] {
		t.Fatalf("pause = %+v: the suspend and its Services must stay", p)
	}
	if !strings.Contains(errw, "stays suspended") || !strings.Contains(errw, "agentbus resume") {
		t.Fatalf("recovery not said: %q", errw)
	}
	evs, _, _ := w.b.Recent(context.Background(), []string{"notify"}, 5)
	if len(evs) == 0 || evs[len(evs)-1].For != bus.ForPerson || !strings.Contains(evs[len(evs)-1].Message, "agentbus resume") {
		t.Fatalf("person notice = %+v", evs)
	}
}

func TestSuspendForceRecordsTheBusyAgents(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	code, out, errw := w.run("--wait", "50ms", "--force")
	if code != 0 {
		t.Fatalf("suspend --force = %d\n%s\n%s", code, out, errw)
	}
	if p := w.pause(); p == nil || !p.Suspended() {
		t.Fatalf("pause = %+v", p)
	}
	if snap := readSnapshot(t, w.state); snap.Agents["coder"].BusyAtSuspend != "working" {
		t.Fatalf("snapshot coder = %+v", snap.Agents["coder"])
	}
}

// --no-wait (and suspend.wait = 0) still pause with Services first, then
// judge the quiet rule's minimum and cancel or, with --force, go on
// (reviewer-codex).
func TestSuspendNoWait(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "idle")
	if code, out, errw := w.run("--no-wait"); code != 0 {
		t.Fatalf("--no-wait on a quiet project = %d\n%s\n%s", code, out, errw)
	}
	w2 := newSuspendWorld(t)
	w2.agent("coder", "working")
	w2.env["AGENT_BUS_SUSPEND_WAIT"] = "0"
	if code, _, _ := w2.run(); code != 1 {
		t.Fatalf("suspend.wait = 0 on a busy project = %d, want 1", code)
	}
	if p := w2.pause(); p != nil {
		t.Fatalf("still paused: %+v", p)
	}
}

func TestSuspendSignalCancels(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "working")
	w.onRound = func(n int) {
		if n == 2 {
			w.sigs <- os.Interrupt
		}
	}
	code, _, errw := w.run("--wait", "5s")
	if code != 1 || !strings.Contains(errw, "interrupted") {
		t.Fatalf("suspend = %d, %q", code, errw)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("still paused after the interrupt: %+v", p)
	}
	if !w.mgr.active[w.b.Project()+"/dispatch"] {
		t.Fatal("dispatch not started again")
	}
}

// A retry on a suspended project with no snapshot completes it and never
// rewrites Services, even after every service was stopped (reviewer-codex).
func TestSuspendRetryKeepsServices(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "idle")
	if code, _, _ := w.run(); code != 0 {
		t.Fatal("first suspend failed")
	}
	if err := os.Remove(filepath.Join(w.state, "suspend.json")); err != nil {
		t.Fatal(err)
	}
	if code, out, errw := w.run(); code != 0 {
		t.Fatalf("retry = %d\n%s\n%s", code, out, errw)
	}
	if p := w.pause(); p == nil || !p.Services["dispatch"] || !p.Services["missions"] {
		t.Fatalf("Services rewritten: %+v", p)
	}
	if snap := readSnapshot(t, w.state); !snap.Services["dispatch"].WasActive {
		t.Fatalf("snapshot services = %+v", snap.Services)
	}
	// With a current snapshot, a third run changes nothing.
	calls := len(w.mgr.calls)
	if code, out, _ := w.run(); code != 0 || !strings.Contains(out, "already suspended") || len(w.mgr.calls) != calls {
		t.Fatalf("third run = %d %q, calls %v", code, out, w.mgr.calls)
	}
}

func TestSuspendRefusesAnIntentAndAnAgent(t *testing.T) {
	w := newSuspendWorld(t)
	w.agent("coder", "idle")
	if err := w.b.BeginSwap(context.Background(), "coder", bus.SwapIntent{Kind: bus.SwapKindReload, By: "sentinel"}); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := w.run("--force"); code == 0 || !strings.Contains(errw, "in flight") {
		t.Fatalf("suspend under an intent = %d %q", code, errw)
	}
	if p := w.pause(); p != nil {
		t.Fatalf("paused under an intent: %+v", p)
	}
	w.env["AGENT_BUS_AGENT"] = "coder"
	if code, _, errw := w.run(); code == 0 || !strings.Contains(errw, "person") {
		t.Fatalf("an agent caller = %d %q", code, errw)
	}
}

// The snapshot carries identifiers, states and counts only: no message text.
func TestSuspendSnapshotHoldsNoText(t *testing.T) {
	w := newSuspendWorld(t)
	ctx := context.Background()
	const marker = "SECRET-MARKER-TEXT"
	w.agent("coder", "idle")
	if _, err := w.b.Status(ctx, "coder", "idle", marker, selfIdent("p-coder")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", marker); err != nil {
		t.Fatal(err)
	}
	if code, out, errw := w.run("--reason", "token=abcdefghijklmnopqrstuvwxyz0123456789"); code != 0 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	raw, err := os.ReadFile(filepath.Join(w.state, "suspend.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) || strings.Contains(string(raw), "abcdefghijklmnop") {
		t.Fatalf("snapshot holds text: %s", raw)
	}
	if snap := readSnapshot(t, w.state); snap.Open.Waiting["coder"] != 1 {
		t.Fatalf("waiting = %+v", snap.Open.Waiting)
	}
}

func readSnapshot(t *testing.T, dir string) suspendSnapshot {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "suspend.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s suspendSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
