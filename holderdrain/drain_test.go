package holderdrain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// fakeLedger is the bus as the drain sees it.
type fakeLedger struct {
	agents  map[string]bus.AgentSnapshot
	resumes map[string]*bus.ResumeRecord
	intents map[string]bus.SwapIntent
	swaps   []bus.SwapEvent // begin, failed, reloaded, in order
	ended   []string
	wakes   map[string][]string // per agent, the wake states the next reads return (the last one stays)
	events  []bus.TermEvent
	busy    map[string]bool // a swap already holds the role
}

func (l *fakeLedger) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	out := map[string]bus.AgentSnapshot{}
	for k, v := range l.agents {
		out[k] = v
	}
	return out, nil
}
func (l *fakeLedger) ResumeOf(_ context.Context, a string) (*bus.ResumeRecord, error) {
	return l.resumes[a], nil
}
func (l *fakeLedger) BeginSwap(_ context.Context, role string, in bus.SwapIntent, ev ...bus.SwapEvent) error {
	if l.busy[role] {
		return bus.ErrSwapInProgress
	}
	if _, held := l.intents[role]; held {
		return bus.ErrSwapInProgress
	}
	l.intents[role] = in
	e := ev[0]
	e.Role, e.Event = role, bus.SwapBegin
	l.swaps = append(l.swaps, e)
	return nil
}
func (l *fakeLedger) EndSwap(_ context.Context, role string) error {
	delete(l.intents, role)
	l.ended = append(l.ended, role)
	return nil
}
func (l *fakeLedger) LogSwap(_ context.Context, e bus.SwapEvent) error {
	l.swaps = append(l.swaps, e)
	return nil
}
func (l *fakeLedger) FinishReload(_ context.Context, e bus.SwapEvent) error {
	e.Event, e.Kind = bus.SwapReloaded, bus.SwapKindReload
	l.swaps = append(l.swaps, e)
	return nil
}
func (l *fakeLedger) Wake(_ context.Context, a string) (bus.WakeRecord, bool, error) {
	ws := l.wakes[a]
	if len(ws) == 0 {
		return bus.WakeRecord{}, false, nil
	}
	st := ws[0]
	if len(ws) > 1 {
		l.wakes[a] = ws[1:]
	}
	return bus.WakeRecord{State: st}, true, nil
}
func (l *fakeLedger) AppendEvent(_ context.Context, e bus.TermEvent) (string, error) {
	l.events = append(l.events, e)
	return "1-0", nil
}

// drainWorld is a project on the fake host: terminals started before the
// "upgrade" are the old build's, every later one the current build's. Each
// agent proved itself in its old terminal; a new terminal's agent proves
// itself when started (unless unprov).
type drainWorld struct {
	h        *fake.Host
	l        *fakeLedger
	old      map[termhost.TermID]bool
	ids      map[string]termhost.TermID // each agent's old terminal
	looks    map[string]dispatch.Inspection
	unprov   map[string]bool
	ops      []string // the host's acts, in order
	startErr error
	silent   []string // socket files that do not answer
	gcs      int
	looked   map[string][]time.Duration // each Inspect of an agent, at the world's clock
	slept    []time.Duration
	clock    time.Time
	started  []termhost.Spec
}

func newDrainWorld(t *testing.T, agents ...string) *drainWorld {
	t.Helper()
	w := &drainWorld{h: fake.New(), old: map[termhost.TermID]bool{}, ids: map[string]termhost.TermID{}, looks: map[string]dispatch.Inspection{},
		unprov: map[string]bool{},
		l: &fakeLedger{agents: map[string]bus.AgentSnapshot{}, resumes: map[string]*bus.ResumeRecord{}, intents: map[string]bus.SwapIntent{},
			wakes: map[string][]string{}, busy: map[string]bool{}}}
	for _, a := range agents {
		tm, err := w.h.Start(context.Background(), termhost.Spec{Project: "p", Name: a})
		if err != nil {
			t.Fatal(err)
		}
		w.old[tm.ID], w.ids[a] = true, tm.ID
		w.l.agents[a] = bus.AgentSnapshot{State: "idle", PID: tm.PID, PIDStart: tm.PIDStart, Pane: string(tm.ID)}
	}
	return w
}

// worldHost records the host's acts; a started agent proves itself.
type worldHost struct {
	termhost.Host
	w *drainWorld
}

func (h worldHost) Start(ctx context.Context, sp termhost.Spec) (termhost.Term, error) {
	h.w.ops = append(h.w.ops, "start "+sp.Name)
	h.w.started = append(h.w.started, sp)
	if h.w.startErr != nil {
		return termhost.Term{}, h.w.startErr
	}
	t, err := h.Host.Start(ctx, sp)
	if err == nil && !h.w.unprov[sp.Name] {
		h.w.l.agents[sp.Name] = bus.AgentSnapshot{State: "idle", PID: t.PID, PIDStart: t.PIDStart, Pane: string(t.ID)}
	}
	return t, err
}

func (h worldHost) Close(ctx context.Context, id termhost.TermID) error {
	h.w.ops = append(h.w.ops, "close "+string(id))
	return h.Host.Close(ctx, id)
}

func (h worldHost) Describe(context.Context) (termhost.HostInfo, error) {
	info := termhost.HostInfo{Sockets: []string{"/run/holder-cur.sock", "/run/holder-old.sock"}}
	for _, s := range h.w.silent {
		for i, k := range info.Sockets {
			if k == s {
				info.Sockets = append(info.Sockets[:i], info.Sockets[i+1:]...)
				break
			}
		}
	}
	return info, nil
}

func (w *drainWorld) deps() Deps {
	return Deps{
		Project: "p",
		Host:    worldHost{w.h, w},
		Bus:     w.l,
		Old:     func(t termhost.Term) bool { return w.old[t.ID] },
		Sockets: func() ([]string, error) { return []string{"/run/holder-cur.sock", "/run/holder-old.sock"}, nil },
		Inspect: func(_ context.Context, t termhost.Term) dispatch.Inspection {
			if w.looked == nil {
				w.looked = map[string][]time.Duration{}
			}
			w.looked[t.Name] = append(w.looked[t.Name], w.clock.Sub(time.Time{}))
			if in, ok := w.looks[t.Name]; ok {
				return in
			}
			return idleLook(func(*dispatch.Inspection) {})
		},
		GC:        func() error { w.gcs++; return nil },
		Sleep:     func(d time.Duration) { w.slept = append(w.slept, d); w.clock = w.clock.Add(d) },
		Now:       func() time.Time { return w.clock },
		Window:    10 * time.Second,
		WakeWait:  10 * time.Second,
		ProofWait: time.Minute,
		ProofPoll: time.Second,
	}
}

// idleLook is an idle terminal with no background work, then f.
func idleLook(f func(*dispatch.Inspection)) dispatch.Inspection {
	in := dispatch.Inspection{State: "idle", Background: &agentstate.Judgement{State: agentstate.BackgroundNone}}
	f(&in)
	return in
}

func (w *drainWorld) live(t *testing.T) map[string][]termhost.TermID {
	t.Helper()
	terms, err := w.h.List(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]termhost.TermID{}
	for _, tm := range terms {
		if tm.Alive {
			out[tm.Name] = append(out[tm.Name], tm.ID)
		}
	}
	return out
}

func swapKinds(l *fakeLedger) string {
	var out []string
	for _, e := range l.swaps {
		out = append(out, e.Event+":"+e.Role)
	}
	return strings.Join(out, ",")
}

// Every old terminal is moved by the reload protocol, one agent at a time:
// the reload intent (the dispatcher holds off), the old terminal closed
// BEFORE the new one starts (two clients never run one session), the resume
// session passed, the proof, the reloaded entry, the intent ended; the gc
// once nothing is left on the old build.
func TestHolderDrainMovesEachOldTerminal(t *testing.T) {
	w := newDrainWorld(t, "coder", "foureyes", "architect")
	if err := w.h.Close(context.Background(), w.ids["architect"]); err != nil { // its program ended: nothing to move
		t.Fatal(err)
	}
	w.l.resumes["coder"] = &bus.ResumeRecord{Session: "11111111-2222-3333-4444-555555555555", Profile: "claude"}
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	live := w.live(t)
	for _, a := range []string{"coder", "foureyes"} {
		if len(live[a]) != 1 || w.old[live[a][0]] {
			t.Fatalf("%s: live terminals %v", a, live[a])
		}
	}
	if got := strings.Join(w.ops, ","); got != "close "+string(w.ids["coder"])+",start coder,close "+string(w.ids["foureyes"])+",start foureyes" {
		t.Fatalf("host acts %s", got)
	}
	if w.started[0] != (termhost.Spec{Project: "p", Name: "coder", Profile: "claude", ResumeSession: "11111111-2222-3333-4444-555555555555"}) ||
		w.started[1] != (termhost.Spec{Project: "p", Name: "foureyes"}) {
		t.Fatalf("started %+v", w.started)
	}
	if got := swapKinds(w.l); got != "begin:coder,reloaded:coder,begin:foureyes,reloaded:foureyes" {
		t.Fatalf("ledger %s", got)
	}
	if b := w.l.swaps[0]; b.Kind != bus.SwapKindReload || b.By != "holder-drain" || b.OldPID != w.l.swaps[0].OldPID || b.OldPane != string(w.ids["coder"]) {
		t.Fatalf("begin %+v", b)
	}
	if r := w.l.swaps[1]; r.NewPane != string(live["coder"][0]) || r.NewPID == 0 {
		t.Fatalf("reloaded %+v (live %v)", r, live["coder"])
	}
	if len(w.l.intents) != 0 || strings.Join(w.l.ended, ",") != "coder,foureyes" {
		t.Fatalf("intents left %v, ended %v", w.l.intents, w.l.ended)
	}
	var kinds []string
	for _, e := range w.l.events {
		kinds = append(kinds, e.Kind+":"+e.Agent)
	}
	if got := strings.Join(kinds, ","); got != "relaunch:coder,relaunch:foureyes,holder-drained:" {
		t.Fatalf("events %s", got)
	}
	if w.gcs != 1 {
		t.Fatalf("gc ran %d times", w.gcs)
	}
	if len(w.slept) == 0 || w.slept[0] < 10*time.Second { // two looks a window apart judge background work
		t.Fatalf("no sampling window: %v", w.slept)
	}
	for _, a := range []string{"coder", "foureyes"} { // each looked at once before the window, then after it
		if l := w.looked[a]; len(l) < 2 || l[0] != 0 || l[1] < 10*time.Second {
			t.Fatalf("%s looked at %v", a, l)
		}
	}
}

// What skips an agent: its old terminal stays, nothing is started for it,
// no intent is left, the gc does not run, and the exit says to run it again.
func TestHolderDrainSkips(t *testing.T) {
	cases := map[string]func(w *drainWorld){
		"declared working": func(w *drainWorld) { s := w.l.agents["coder"]; s.State = "working"; w.l.agents["coder"] = s },
		"seen working": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.State = "working" })
		},
		"blocked": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.State = "blocked" })
		},
		"person active": func(w *drainWorld) { w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Focused = true }) },
		"unreadable": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Err = errors.New("x") })
		},
		"gone": func(w *drainWorld) { w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Gone = true }) },
		"background running": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) {
				in.Background = &agentstate.Judgement{State: agentstate.BackgroundRunning}
			})
		},
		"background unknown": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Background = &agentstate.Judgement{} })
		},
		"no judgement": func(w *drainWorld) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Background = nil })
		},
		"draft": func(w *drainWorld) { w.h.SetActivity(w.ids["coder"], termhost.Activity{Draft: true}) },
		"not proved here": func(w *drainWorld) {
			s := w.l.agents["coder"]
			s.PID = 99999
			w.l.agents["coder"] = s
		},
		"unknown to the bus":  func(w *drainWorld) { delete(w.l.agents, "coder") },
		"a swap in progress":  func(w *drainWorld) { w.l.busy["coder"] = true },
		"a wake still typing": func(w *drainWorld) { w.l.wakes["coder"] = []string{"prompting"} },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			w := newDrainWorld(t, "coder", "foureyes")
			set(w)
			var out bytes.Buffer
			if code := Run(context.Background(), w.deps(), &out); code != Skipped {
				t.Fatalf("exit %d, want %d:\n%s", code, Skipped, out.String())
			}
			live := w.live(t)
			if len(live["coder"]) != 1 || live["coder"][0] != w.ids["coder"] {
				t.Fatalf("coder was touched: %v", live["coder"])
			}
			if len(live["foureyes"]) != 1 || w.old[live["foureyes"][0]] {
				t.Fatalf("foureyes was not moved: %v", live["foureyes"])
			}
			for _, sp := range w.started {
				if sp.Name == "coder" {
					t.Fatal("a terminal was started for a skipped agent")
				}
			}
			if w.gcs != 0 || len(w.l.intents) != 0 {
				t.Fatalf("gc %d, intents left %v", w.gcs, w.l.intents)
			}
			if !strings.Contains(out.String(), "coder skipped") {
				t.Fatalf("output:\n%s", out.String())
			}
		})
	}
}

// A wake being typed when the intent is written is waited out, then the
// agent is moved.
func TestHolderDrainWaitsOutAWake(t *testing.T) {
	w := newDrainWorld(t, "coder")
	w.l.wakes["coder"] = []string{"prompting", "prompting", "prompted"}
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

// After the intent, the same idle process is checked again: one that
// changed, or started working, is left as it is.
func TestHolderDrainRechecksAfterTheIntent(t *testing.T) {
	for name, change := range map[string]func(s *bus.AgentSnapshot){
		"another process": func(s *bus.AgentSnapshot) { s.PIDStart++ },
		"now working":     func(s *bus.AgentSnapshot) { s.State = "working" },
	} {
		w := newDrainWorld(t, "coder")
		d := w.deps()
		d.Bus = &changingLedger{fakeLedger: w.l, after: func() { s := w.l.agents["coder"]; change(&s); w.l.agents["coder"] = s }}
		var out bytes.Buffer
		if code := Run(context.Background(), d, &out); code != Skipped || len(w.ops) != 0 {
			t.Fatalf("%s: exit %d, acts %v:\n%s", name, code, w.ops, out.String())
		}
		if got := swapKinds(w.l); got != "begin:coder,failed:coder" || len(w.l.intents) != 0 {
			t.Fatalf("%s: ledger %s, intents %v", name, got, w.l.intents)
		}
	}
}

// changingLedger changes the agent right after the intent is written.
type changingLedger struct {
	*fakeLedger
	after func()
}

func (c *changingLedger) BeginSwap(ctx context.Context, role string, in bus.SwapIntent, ev ...bus.SwapEvent) error {
	err := c.fakeLedger.BeginSwap(ctx, role, in, ev...)
	if err == nil {
		c.after()
	}
	return err
}

// --ignore-background (the person's) moves an agent whose background work
// runs or is unknown; it lifts nothing else.
func TestHolderDrainIgnoreBackground(t *testing.T) {
	for name, look := range map[string]dispatch.Inspection{
		"running": idleLook(func(in *dispatch.Inspection) {
			in.Background = &agentstate.Judgement{State: agentstate.BackgroundRunning}
		}),
		"unknown": idleLook(func(in *dispatch.Inspection) { in.Background = &agentstate.Judgement{} }),
		"nil":     idleLook(func(in *dispatch.Inspection) { in.Background = nil }),
	} {
		w := newDrainWorld(t, "coder")
		w.looks["coder"] = look
		d := w.deps()
		d.IgnoreBackground = true
		var out bytes.Buffer
		if code := Run(context.Background(), d, &out); code != 0 {
			t.Fatalf("%s: exit %d:\n%s", name, code, out.String())
		}
	}
	w := newDrainWorld(t, "coder")
	w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Focused = true })
	d := w.deps()
	d.IgnoreBackground = true
	var out bytes.Buffer
	if code := Run(context.Background(), d, &out); code != Skipped {
		t.Fatalf("--ignore-background lifted a person active: exit %d", code)
	}
}

// A holder socket that does not answer: its terminals are unseen, so the
// drain does nothing and never calls the project drained (reviewer-codex).
func TestHolderDrainNeedsEveryHolder(t *testing.T) {
	w := newDrainWorld(t, "coder")
	w.silent = []string{"/run/holder-old.sock"}
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 1 || len(w.ops) != 0 || w.gcs != 0 || len(w.l.events) != 0 {
		t.Fatalf("exit %d acts %v gc %d events %v:\n%s", code, w.ops, w.gcs, w.l.events, out.String())
	}
	if !strings.Contains(out.String(), "do not answer") {
		t.Fatalf("output:\n%s", out.String())
	}
	// One that goes silent during the drain: moved, but not called drained.
	w = newDrainWorld(t, "coder")
	d := w.deps()
	d.GC = func() error { w.gcs++; return nil }
	silence := d.Host.(worldHost)
	d.Bus = &changingLedger{fakeLedger: w.l, after: func() { w.silent = []string{"/run/holder-old.sock"} }}
	d.Host = silence
	out.Reset()
	if code := Run(context.Background(), d, &out); code != 1 || w.gcs != 0 {
		t.Fatalf("exit %d gc %d:\n%s", code, w.gcs, out.String())
	}
	for _, e := range w.l.events {
		if e.Kind == bus.EventHolderDrained {
			t.Fatal("called drained with a holder silent")
		}
	}
}

// A start that fails after the old terminal closed: exit 1, the failure in
// the ledger with the way back, the intent ended, the drain stopped.
func TestHolderDrainStartFails(t *testing.T) {
	w := newDrainWorld(t, "coder", "foureyes")
	w.startErr = errors.New("refused")
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 1 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if got := swapKinds(w.l); got != "begin:coder,failed:coder" || !strings.Contains(w.l.swaps[1].Error, "start:") || len(w.l.intents) != 0 {
		t.Fatalf("ledger %s %+v", got, w.l.swaps)
	}
	if !strings.Contains(out.String(), "agentbus up p --resume") || w.gcs != 0 {
		t.Fatalf("output:\n%s", out.String())
	}
	if live := w.live(t); len(live["foureyes"]) != 1 || live["foureyes"][0] != w.ids["foureyes"] {
		t.Fatalf("the drain went on: %v", live)
	}
}

// A new terminal that never proves itself is left running (the old one is
// already closed: closing it would leave no agent); exit 1, said so.
func TestHolderDrainUnproved(t *testing.T) {
	w := newDrainWorld(t, "coder")
	w.unprov["coder"] = true
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 1 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	live := w.live(t)
	if len(live["coder"]) != 1 || w.old[live["coder"][0]] {
		t.Fatalf("coder: %v", live["coder"])
	}
	if got := swapKinds(w.l); got != "begin:coder,failed:coder" || !strings.Contains(out.String(), "left running") || w.gcs != 0 || len(w.l.intents) != 0 {
		t.Fatalf("ledger %s gc %d intents %v:\n%s", got, w.gcs, w.l.intents, out.String())
	}
}

// Nothing on an old build: nothing started, no window waited, the gc runs.
func TestHolderDrainNothingToDo(t *testing.T) {
	w := newDrainWorld(t)
	if _, err := w.h.Start(context.Background(), termhost.Spec{Project: "p", Name: "coder"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 0 || len(w.started) != 0 || w.gcs != 1 || len(w.slept) != 0 {
		t.Fatalf("exit %d started %v gc %d slept %v:\n%s", code, w.started, w.gcs, w.slept, out.String())
	}
}

// inTerm: the agent's verified process is the terminal's program or in its
// tree, with the same start time; never another process.
func TestInTerm(t *testing.T) {
	h := fake.New()
	nt, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	h.SetProcesses(nt.ID, termhost.Proc{PID: 4242, PPID: nt.PID, Start: 77})
	for _, c := range []struct {
		name string
		s    bus.AgentSnapshot
		want bool
	}{
		{"the program", bus.AgentSnapshot{PID: nt.PID, PIDStart: nt.PIDStart}, true},
		{"a descendant", bus.AgentSnapshot{PID: 4242, PIDStart: 77}, true},
		{"a descendant's pid, another start", bus.AgentSnapshot{PID: 4242, PIDStart: 78}, false},
		{"the program's pid, another start", bus.AgentSnapshot{PID: nt.PID, PIDStart: nt.PIDStart + 1}, false},
		{"another process", bus.AgentSnapshot{PID: 9, PIDStart: 1}, false},
		{"unverified", bus.AgentSnapshot{}, false},
	} {
		if got, err := inTerm(context.Background(), h, c.s, nt); got != c.want || err != nil {
			t.Errorf("%s: %v %v", c.name, got, err)
		}
	}
}

// A terminal that appears on an old build during the drain (an old holder
// still takes one) leaves the project not drained: no gc, no event.
func TestHolderDrainNewOldTerminal(t *testing.T) {
	w := newDrainWorld(t, "coder")
	d := w.deps()
	d.Bus = &changingLedger{fakeLedger: w.l, after: func() {
		tm, err := w.h.Start(context.Background(), termhost.Spec{Project: "p", Name: "sentinel"})
		if err != nil {
			t.Fatal(err)
		}
		w.old[tm.ID] = true
	}}
	var out bytes.Buffer
	if code := Run(context.Background(), d, &out); code != 1 || w.gcs != 0 {
		t.Fatalf("exit %d gc %d:\n%s", code, w.gcs, out.String())
	}
	for _, e := range w.l.events {
		if e.Kind == bus.EventHolderDrained {
			t.Fatal("called drained with an old terminal left")
		}
	}
}
