package main

import (
	"context"
	"errors"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// Item 22: an agent idle in its snapshot whose terminal is gone from the
// holder shows 'no terminal' in the Agents view, never idle; once a live
// terminal of it is back, its own state again. Its snapshot is untouched.
func TestAgentWithoutATerminalShowsNoTerminal(t *testing.T) {
	h := fake.New()
	coder, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(context.Background(), gone.ID); err != nil { // architect's terminal ended
		t.Fatal(err)
	}
	var w hostWatcher
	facts, err := w.read(context.Background(), &eventsStub{}, h, "p")
	if err != nil {
		t.Fatal(err)
	}
	st := &shared{agents: map[string]*agentState{
		"coder":     {state: "idle", pane: string(coder.ID)},
		"architect": {state: "idle", pane: string(gone.ID)},
		"security":  {state: "idle", pane: "t9"}, // never on this holder at all
		"person":    {state: "idle"},             // no terminal recorded: a person
	}}
	applyHostFacts(st, facts, w.termsRead)
	for name, want := range map[string]string{"coder": "idle", "architect": bus.StateNoTerminal, "security": bus.StateNoTerminal, "person": "idle"} {
		if _, word, _, _ := stateLook(st.agents[name]); word != want {
			t.Errorf("%s: %q, want %q", name, word, want)
		}
	}
	if st.agents["architect"].state != "idle" {
		t.Fatal("the declared state was rewritten")
	}
	// The holder cannot be read: nothing is known, the declared state shows.
	w2 := hostWatcher{}
	facts, _ = w2.read(context.Background(), &eventsStub{}, downHolder{}, "p")
	applyHostFacts(st, facts, w2.termsRead)
	if _, word, _, _ := stateLook(st.agents["architect"]); word != "idle" {
		t.Fatalf("an unread holder: %q", word)
	}
	// No holder of the project answers on this host: an empty list, which
	// proves nothing.
	w3 := hostWatcher{}
	facts, _ = w3.read(context.Background(), &eventsStub{}, noHolder{}, "p")
	applyHostFacts(st, facts, w3.termsRead)
	if _, word, _, _ := stateLook(st.agents["architect"]); word != "idle" {
		t.Fatalf("no holder answers: %q", word)
	}
	// Its terminal back: its own state again.
	if _, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: "architect"}); err != nil {
		t.Fatal(err)
	}
	facts, _ = w.read(context.Background(), &eventsStub{}, h, "p")
	applyHostFacts(st, facts, w.termsRead)
	if _, word, _, _ := stateLook(st.agents["architect"]); word != "idle" {
		t.Fatalf("back: %q", word)
	}
}

// downHolder is a holder that cannot be listed.
type downHolder struct{}

func (downHolder) List(context.Context, string) ([]termhost.Term, error) {
	return nil, errors.New("holder down")
}
func (downHolder) Activity(context.Context, termhost.TermID) (termhost.Activity, error) {
	return termhost.Activity{}, nil
}

// noHolder lists nothing, and no holder answers (this host runs none).
type noHolder struct{}

func (noHolder) List(context.Context, string) ([]termhost.Term, error) { return nil, nil }
func (noHolder) Activity(context.Context, termhost.TermID) (termhost.Activity, error) {
	return termhost.Activity{}, nil
}
func (noHolder) Describe(context.Context) (termhost.HostInfo, error) { return termhost.HostInfo{}, nil }
