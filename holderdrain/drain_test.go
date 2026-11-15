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

// drainWorld is a project on the fake host: terminals started before
// "upgrade" are the old build's, every later one the current build's.
type drainWorld struct {
	h       *fake.Host
	old     map[termhost.TermID]bool
	looks   map[string]dispatch.Inspection // per agent (default idle)
	state   map[string]string              // declared states
	resumes map[string]*bus.ResumeRecord
	unprov  map[string]bool // agents whose new terminal never proves itself
	events  []bus.TermEvent
	gcs     int
	slept   []time.Duration
	started []termhost.Spec
	clock   time.Time
}

func newDrainWorld(t *testing.T, agents ...string) *drainWorld {
	t.Helper()
	w := &drainWorld{h: fake.New(), old: map[termhost.TermID]bool{}, looks: map[string]dispatch.Inspection{},
		state: map[string]string{}, resumes: map[string]*bus.ResumeRecord{}, unprov: map[string]bool{}}
	for _, a := range agents {
		tm, err := w.h.Start(context.Background(), termhost.Spec{Project: "p", Name: a})
		if err != nil {
			t.Fatal(err)
		}
		w.old[tm.ID] = true
	}
	return w
}

func (w *drainWorld) deps() Deps {
	return Deps{
		Project: "p",
		Host:    startRecorder{w.h, &w.started},
		Old:     func(t termhost.Term) bool { return w.old[t.ID] },
		Inspect: func(_ context.Context, t termhost.Term) dispatch.Inspection {
			if in, ok := w.looks[t.Name]; ok {
				return in
			}
			return dispatch.Inspection{State: "idle", Background: &agentstate.Judgement{State: agentstate.BackgroundNone}}
		},
		Declared: func(context.Context) (map[string]string, error) { return w.state, nil },
		Resume:   func(_ context.Context, a string) (*bus.ResumeRecord, error) { return w.resumes[a], nil },
		Proved: func(_ context.Context, a string, _ termhost.Term) (bool, error) {
			return !w.unprov[a], nil
		},
		Event:     func(_ context.Context, e bus.TermEvent) error { w.events = append(w.events, e); return nil },
		GC:        func() error { w.gcs++; return nil },
		Sleep:     func(d time.Duration) { w.slept = append(w.slept, d); w.clock = w.clock.Add(d) },
		Now:       func() time.Time { return w.clock },
		Window:    10 * time.Second,
		ProofWait: time.Minute,
		ProofPoll: time.Second,
	}
}

// startRecorder is the host, the specs it was asked to start recorded.
type startRecorder struct {
	termhost.Host
	specs *[]termhost.Spec
}

func (s startRecorder) Start(ctx context.Context, sp termhost.Spec) (termhost.Term, error) {
	*s.specs = append(*s.specs, sp)
	return s.Host.Start(ctx, sp)
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

// Every old terminal is relaunched on the current build, one at a time, its
// resume session passed to Start, the old one closed only once the new one
// is proved; the gc runs once nothing is left on the old build.
func TestHolderDrainRelaunchesEachOldTerminal(t *testing.T) {
	w := newDrainWorld(t, "coder", "foureyes", "architect")
	for id := range w.old { // architect's program ended on the old build: nothing to relaunch
		if tm := w.live(t)["architect"]; len(tm) == 1 && tm[0] == id {
			if err := w.h.Close(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	w.resumes["coder"] = &bus.ResumeRecord{Session: "11111111-2222-3333-4444-555555555555", Profile: "claude"}
	var out bytes.Buffer
	code := Run(context.Background(), w.deps(), &out)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	live := w.live(t)
	for _, a := range []string{"coder", "foureyes"} {
		if len(live[a]) != 1 || w.old[live[a][0]] {
			t.Fatalf("%s: live terminals %v (old %v)", a, live[a], w.old)
		}
	}
	if len(w.started) != 2 || w.started[0] != (termhost.Spec{Project: "p", Name: "coder", Profile: "claude", ResumeSession: "11111111-2222-3333-4444-555555555555"}) ||
		w.started[1] != (termhost.Spec{Project: "p", Name: "foureyes"}) {
		t.Fatalf("started %+v", w.started)
	}
	if w.gcs != 1 {
		t.Fatalf("gc ran %d times", w.gcs)
	}
	var kinds []string
	for _, e := range w.events {
		kinds = append(kinds, e.Kind+":"+e.Agent)
	}
	if got := strings.Join(kinds, ","); got != "relaunch:coder,relaunch:foureyes,holder-drained:" {
		t.Fatalf("events %s", got)
	}
	// The background judgement needs two samples a window apart.
	if len(w.slept) == 0 || w.slept[0] < 10*time.Second {
		t.Fatalf("no sampling window: %v", w.slept)
	}
}

// An agent working (declared or seen), with a person active, a draft, or
// background work is skipped: its old terminal stays, nothing is started,
// the gc does not run, and the exit says to run it again.
func TestHolderDrainSkips(t *testing.T) {
	cases := map[string]func(w *drainWorld, id termhost.TermID){
		"declared working": func(w *drainWorld, _ termhost.TermID) { w.state["coder"] = "working" },
		"seen working": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.State = "working" })
		},
		"blocked": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.State = "blocked" })
		},
		"person active": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Focused = true })
		},
		"background unknown": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Background = &agentstate.Judgement{} })
		},
		"no judgement": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Background = nil })
		},
		"unknown look": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) { in.Err = errors.New("x") })
		},
		"draft": func(w *drainWorld, id termhost.TermID) {
			w.h.SetActivity(id, termhost.Activity{Draft: true})
		},
		"background": func(w *drainWorld, _ termhost.TermID) {
			w.looks["coder"] = idleLook(func(in *dispatch.Inspection) {
				in.Background = &agentstate.Judgement{State: agentstate.BackgroundRunning}
			})
		},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			w := newDrainWorld(t, "coder", "foureyes")
			var coderID termhost.TermID
			for id := range w.old {
				if tm := w.live(t)["coder"]; len(tm) == 1 && tm[0] == id {
					coderID = id
				}
			}
			set(w, coderID)
			var out bytes.Buffer
			if code := Run(context.Background(), w.deps(), &out); code != 3 {
				t.Fatalf("exit %d, want 3 (skipped):\n%s", code, out.String())
			}
			live := w.live(t)
			if len(live["coder"]) != 1 || !w.old[live["coder"][0]] {
				t.Fatalf("coder was relaunched: %v", live["coder"])
			}
			if len(live["foureyes"]) != 1 || w.old[live["foureyes"][0]] {
				t.Fatalf("foureyes was not relaunched: %v", live["foureyes"])
			}
			if w.gcs != 0 {
				t.Fatal("the gc ran with an old terminal left")
			}
			if !strings.Contains(out.String(), "coder skipped") {
				t.Fatalf("output:\n%s", out.String())
			}
		})
	}
}

// A new terminal that never proves itself is closed, the old one kept, and
// the drain stops there.
func TestHolderDrainUnprovedKeepsTheOld(t *testing.T) {
	w := newDrainWorld(t, "coder", "foureyes")
	w.unprov["coder"] = true
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 1 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	live := w.live(t)
	if len(live["coder"]) != 1 || !w.old[live["coder"][0]] {
		t.Fatalf("coder: %v", live["coder"])
	}
	if len(live["foureyes"]) != 1 || !w.old[live["foureyes"][0]] {
		t.Fatalf("the drain went on after a failure: %v", live["foureyes"])
	}
	if w.gcs != 0 || !strings.Contains(out.String(), "not proved") {
		t.Fatalf("gc %d, output:\n%s", w.gcs, out.String())
	}
}

// Nothing on an old build: nothing started, the gc still runs.
func TestHolderDrainNothingToDo(t *testing.T) {
	w := newDrainWorld(t)
	if _, err := w.h.Start(context.Background(), termhost.Spec{Project: "p", Name: "coder"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(context.Background(), w.deps(), &out); code != 0 || len(w.started) != 0 || w.gcs != 1 {
		t.Fatalf("exit %d started %v gc %d:\n%s", code, w.started, w.gcs, out.String())
	}
	if len(w.slept) != 0 {
		t.Fatal("waited a sampling window with nothing to drain")
	}
}

// idleLook is an idle terminal with no background work, then f.
func idleLook(f func(*dispatch.Inspection)) dispatch.Inspection {
	in := dispatch.Inspection{State: "idle", Background: &agentstate.Judgement{State: agentstate.BackgroundNone}}
	f(&in)
	return in
}

// --ignore-background (the person's) relaunches an agent whose background
// work runs or is unknown; it lifts nothing else.
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

// The proof: the agent's verified process is the new terminal's program or
// in its process tree, with the same start time; never another process.
func TestProvedIn(t *testing.T) {
	h := fake.New()
	nt, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	h.SetProcesses(nt.ID, termhost.Proc{PID: 4242, PPID: nt.PID, Start: 77})
	snap := bus.AgentSnapshot{}
	agents := func(context.Context) (map[string]bus.AgentSnapshot, error) {
		return map[string]bus.AgentSnapshot{"coder": snap}, nil
	}
	proved := ProvedIn(h, agents)
	for _, c := range []struct {
		name string
		s    bus.AgentSnapshot
		want bool
	}{
		{"the program", bus.AgentSnapshot{PID: nt.PID, PIDStart: nt.PIDStart}, true},
		{"a descendant", bus.AgentSnapshot{PID: 4242, PIDStart: 77}, true},
		{"a descendant's pid, another start", bus.AgentSnapshot{PID: 4242, PIDStart: 78}, false},
		{"the program's pid, another start", bus.AgentSnapshot{PID: nt.PID, PIDStart: nt.PIDStart + 1}, false},
		{"another process (the old terminal's)", bus.AgentSnapshot{PID: 9, PIDStart: 1}, false},
		{"unverified", bus.AgentSnapshot{}, false},
	} {
		snap = c.s
		if got, err := proved(context.Background(), "coder", nt); got != c.want || err != nil {
			t.Errorf("%s: %v %v", c.name, got, err)
		}
	}
	if got, _ := proved(context.Background(), "foureyes", nt); got {
		t.Error("an agent without a snapshot is proved")
	}
}
