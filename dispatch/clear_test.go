package dispatch

import (
	"errors"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

var errTest = errors.New("test")

// clearFacts is one agent ready to be cleared: every rule of part B passes.
func clearFacts() ClearFacts {
	snap := bus.AgentSnapshot{State: "idle", TS: 2000, PID: testPID, PIDStart: testStart, IdentSession: "session-0001", Client: "claude-code", Pane: "w1:p2"}
	return ClearFacts{
		On:     true,
		Record: bus.Handoff{Report: "1-0", Session: "session-0001", PID: testPID, PIDStart: testStart, At: 1000},
		Snap:   snap,
		Inspection: Inspection{State: "idle", Client: "claude", Ready: true, PIDs: []int{testPID},
			Starts: map[int]uint64{testPID: testStart}},
	}
}

// Each condition of part B's rule vetoes the clear on its own; with all of
// them met it clears (lifecycle spec, part B).
func TestDecideClearEveryConditionVetoesAlone(t *testing.T) {
	if d := DecideClear(clearFacts()); !d.Clear {
		t.Fatalf("all conditions met, not cleared: %s", d.Reason)
	}
	// The declaration writes the agent idle at the record's own time: that
	// idle is "since its hand-off" (chain-reliability).
	f0 := clearFacts()
	f0.Snap.TS = f0.Record.At
	if d := DecideClear(f0); !d.Clear {
		t.Fatalf("idle at the declaration, not cleared: %s", d.Reason)
	}
	for name, c := range map[string]func(*ClearFacts){
		"switched off":             func(f *ClearFacts) { f.On = false },
		"another client":           func(f *ClearFacts) { f.Snap.Client = "codex" },
		"no client recorded":       func(f *ClearFacts) { f.Snap.Client = "" },
		"void record":              func(f *ClearFacts) { f.Snap.IdentSession = "session-0002" },
		"paused":                   func(f *ClearFacts) { f.Paused = true },
		"swap or reload intent":    func(f *ClearFacts) { f.Swapping = true },
		"provider held":            func(f *ClearFacts) { f.Held = true },
		"open ask":                 func(f *ClearFacts) { f.OpenAsk = true },
		"a gate on either side":    func(f *ClearFacts) { f.Gates = []string{"challenge x gates coder"} },
		"not idle":                 func(f *ClearFacts) { f.Snap.State = "blocked" },
		"idle before the record":   func(f *ClearFacts) { f.Snap.TS = 999 },
		"armed":                    func(f *ClearFacts) { f.Armed = true },
		"a wake being typed":       func(f *ClearFacts) { f.WakeState = "prompting" },
		"cleared once already":     func(f *ClearFacts) { f.Marked = true },
		"identity not in pane":     func(f *ClearFacts) { f.Inspection.PIDs = []int{99} },
		"focused":                  func(f *ClearFacts) { f.Inspection.Focused = true },
		"not ready":                func(f *ClearFacts) { f.Inspection.Ready = false },
		"busy":                     func(f *ClearFacts) { f.Inspection.Busy = true },
		"pane blocked on a prompt": func(f *ClearFacts) { f.Inspection.State = "blocked" },
		"inspection failed":        func(f *ClearFacts) { f.Inspection.Err = errTest },
	} {
		f := clearFacts()
		c(&f)
		if d := DecideClear(f); d.Clear || d.Reason == "" {
			t.Errorf("%s: cleared (%+v)", name, d)
		}
	}
	// A gate is reported, so the escalation target can close or answer it.
	f := clearFacts()
	f.Gates = []string{"challenge x gates coder"}
	if d := DecideClear(f); len(d.WaitGates) != 1 {
		t.Fatalf("gates not reported: %+v", d)
	}
}
