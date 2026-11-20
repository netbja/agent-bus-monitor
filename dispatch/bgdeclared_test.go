package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
)

// background-work-visible §4b: the agent's declared text holds the /clear
// and the pop's end, as running work does; once the agent's context episode
// has escalated it no longer holds the /clear, while observed work still
// does; the pop's end keeps honouring it.
func TestDeclaredTextHoldsWithinTheContextBound(t *testing.T) {
	bg := agentstate.Background{Authoritative: []string{agentstate.SourceProcess}}
	text := "nightly build"
	idle := &agentstate.ProcessFacts{Window: 10 * time.Second}
	declared := bg.Judge(agentstate.Facts{Process: idle, Declared: &text}, time.Now())
	busy := bg.Judge(agentstate.Facts{Process: &agentstate.ProcessFacts{Window: 10 * time.Second,
		Procs: []agentstate.Proc{{Name: "make"}}}, Declared: &text}, time.Now())

	f := clearFacts()
	f.Inspection.Background = &declared
	if d := DecideClear(f); d.Clear || d.Reason != "background work: declared: nightly build" {
		t.Fatalf("declared: %+v", d)
	}
	f.ContextEscalated = true
	if d := DecideClear(f); !d.Clear {
		t.Fatalf("escalated: the declaration still held the /clear: %+v", d)
	}
	f.Inspection.Background = &busy
	if d := DecideClear(f); d.Clear || d.Reason != "background work: process 'make'" {
		t.Fatalf("escalated, observed work: %+v", d)
	}
	p := donePop()
	p.Pane.Background = &declared
	if done, _ := PopDone(p); done {
		t.Fatal("the pop ended over a declaration")
	}
}

// The runtime reads the declaration as a source (HostRuntime.Declared).
func TestHostRuntimeReadsTheDeclaration(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Declared = func(agent string) (string, bool) { return "2 checks running", agent == f.term.Name }
	if in := f.inspect(); in.BackgroundSkip() != "background work: declared: 2 checks running" || in.Background.Source() != agentstate.SourceDeclared {
		t.Fatalf("declared: %q", in.BackgroundSkip())
	}
}

// DeclaredFrom reads the record on the bus: set, then cleared.
func TestDeclaredFromTheBus(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	read := DeclaredFrom(b)
	if _, ok := read("coder"); ok {
		t.Fatal("no record read as a declaration")
	}
	if err := b.SetBackground(ctx, "coder", "2 checks running", "coder", time.Minute); err != nil {
		t.Fatal(err)
	}
	if text, ok := read("coder"); !ok || text != "2 checks running" {
		t.Fatalf("read %q %v", text, ok)
	}
	if err := b.ClearBackground(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if _, ok := read("coder"); ok {
		t.Fatal("a cleared record still read")
	}
}
