package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// judged is a host's background judgement: running (a 'pytest' child), none
// (the process tree read, nothing in it) or unknown (no authoritative source
// read).
func judged(state string) *agentstate.Judgement {
	bg := agentstate.Background{Authoritative: []string{agentstate.SourceProcess}}
	f := agentstate.Facts{Process: &agentstate.ProcessFacts{Window: 10 * time.Second}}
	switch state {
	case "running":
		f.Process.Procs = []agentstate.Proc{{Name: "pytest"}}
	case "unknown":
		f.Process = nil
	}
	j := bg.Judge(f, time.Now())
	return &j
}

// herdr exit 4.3b: background work on a host skips the auto-clear and the
// pop's end; unknown counts as running (fail closed); none, and herdr's
// Runtime (no judgement), skip nothing.
func TestBackgroundWorkSkipsClearAndPopEnd(t *testing.T) {
	for _, c := range []struct {
		state string
		skip  string
	}{
		{"running", "background work: process 'pytest'"},
		{"unknown", "background work: unknown"},
		{"none", ""},
		{"herdr", ""},
	} {
		var j *agentstate.Judgement
		if c.state != "herdr" {
			j = judged(c.state)
		}
		f := clearFacts()
		f.Inspection.Background = j
		d := DecideClear(f)
		if c.skip == "" && !d.Clear {
			t.Errorf("%s: clear refused: %s", c.state, d.Reason)
		}
		if c.skip != "" && (d.Clear || d.Reason != c.skip) {
			t.Errorf("%s: clear %v (%q), want skipped: %q", c.state, d.Clear, d.Reason, c.skip)
		}
		p := donePop()
		p.Pane.Background = j
		done, why := PopDone(p)
		if c.skip == "" && !done {
			t.Errorf("%s: pop kept: %s", c.state, why)
		}
		if c.skip != "" && (done || !strings.Contains(why, c.skip)) {
			t.Errorf("%s: pop done %v (%q), want kept for %q", c.state, done, why, c.skip)
		}
	}
}

// The auto-clear skips an agent whose host sees background work, records the
// skip once per change of what runs ({p}:events), and clears once it ends.
func TestClearPassSkipsBackgroundWork(t *testing.T) {
	w := newClearWorld(t, true)
	w.l.cfg.Clear.Decisions = NewClearDecisions()
	bg := judged("running")
	w.rt.script = func(string, int) Inspection { in := readyIn(); in.Background = bg; return in }
	w.pass()
	w.pass()
	if n := w.clears(); n != 0 {
		t.Fatalf("%d clears with background work", n)
	}
	evs, err := w.b.RecentEvents(context.Background(), 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != bus.EventBackgroundSkip ||
		evs[0].Detail != "auto-clear skipped — background work: process 'pytest'" || evs[0].By != "dispatch" {
		t.Fatalf("events %+v %v", evs, err)
	}
	if got := w.l.cfg.Clear.Decisions.Last("coder"); got != "background work: process 'pytest'" {
		t.Fatalf("recorded reason %q", got)
	}
	bg = judged("none")
	w.pass()
	if n := w.clears(); n != 1 {
		t.Fatalf("%d clears once the work ended", n)
	}
}

// A /clear pasted and not seen in the input (spec 4.2): the person is told
// it may be left there, as submitting it clears the conversation.
func TestClearNotLandedTellsThePerson(t *testing.T) {
	w := newClearWorld(t, true)
	w.rt.clearErr = termhost.ErrNotLanded
	snaps, err := w.b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = w.l.clearPass(context.Background(), snaps) // the clear's error is logged by the pass
	ns, _, err := w.b.Recent(context.Background(), []string{"notify"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range ns {
		if n.Message == NotLandedText("coder", "clear") && n.For == bus.ForPerson {
			return
		}
	}
	t.Fatalf("no notice for the person: %+v", ns)
}
