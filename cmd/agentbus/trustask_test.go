package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fakeAsks is the broker's asks for up's trust reminder (first-run-trust (b)).
type fakeAsks struct {
	asks      []bus.Ask
	defs      []bus.AskDef
	withdrawn []string // agent|id|qid|note
	err       error
}

func (f *fakeAsks) Ask(_ context.Context, def bus.AskDef, _ string) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	f.defs = append(f.defs, def)
	id := "ask-" + def.From
	var qs []bus.AskQuestionState
	for _, q := range def.Questions {
		qs = append(qs, bus.AskQuestionState{AskQuestion: q})
	}
	f.asks = append(f.asks, bus.Ask{ID: id, From: def.From, Title: def.Title, State: "open", Questions: qs})
	return id, "open", nil
}

func (f *fakeAsks) Asks(context.Context) ([]bus.Ask, error) { return f.asks, f.err }

func (f *fakeAsks) WithdrawQuestion(_ context.Context, agent, id, qid, note string) (int, error) {
	f.withdrawn = append(f.withdrawn, agent+"|"+id+"|"+qid+"|"+note)
	for i := range f.asks {
		if f.asks[i].ID == id {
			f.asks[i].State = "answered"
		}
	}
	return 0, nil
}

func withAsks(deps upDeps, f *fakeAsks) upDeps {
	deps.TrustAsks = func(project string) (trustAskBus, error) {
		if project != "demo" {
			return nil, errors.New("wrong project " + project)
		}
		return f, nil
	}
	return deps
}

// An untrusted gated client gets one ask in For you, from its first agent:
// the folder, where to answer, the choice by its label and what trusting
// loads; it closes itself once the trust is accepted during up's wait. A
// trusted client gets none. Nothing is written to a client's file.
func TestUpOpensATrustAskAndClosesIt(t *testing.T) {
	work, _, deps, _ := gateFixture(t, 2)
	f := &fakeAsks{}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(f.defs) != 1 {
		t.Fatalf("asks %+v, want one for Claude Code", f.defs)
	}
	d := f.defs[0]
	q := d.Questions[0]
	if d.From != "master" || q.ID != "trust-claude-code" || bus.ValidateAskDef(d) != nil ||
		!strings.Contains(q.Question, `choose "Yes, I trust this folder"`) || !strings.Contains(q.Question, "master's pane") ||
		!strings.Contains(q.Context, work) || !strings.Contains(q.Context, "hooks") {
		t.Fatalf("ask %+v (valid: %v)", d, bus.ValidateAskDef(d))
	}
	if len(f.withdrawn) != 1 || !strings.HasPrefix(f.withdrawn[0], "master|ask-master|trust-claude-code|") || !strings.Contains(f.withdrawn[0], "trusted") {
		t.Fatalf("withdrawn %q", f.withdrawn)
	}
}

// Not accepted in time: the ask stays open for the person.
func TestUpTrustAskStaysOpenOnTimeout(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	f := &fakeAsks{}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || len(f.withdrawn) != 0 {
		t.Fatalf("defs %d withdrawn %q", len(f.defs), f.withdrawn)
	}
}

// A later up closes a trust ask whose folder is now trusted, and opens no
// second one while an ask for the same client is still open.
func TestUpSettlesItsTrustAsks(t *testing.T) {
	work, _, deps, _ := gateFixture(t, 0) // trusted from the start
	open := bus.Ask{ID: "ask-master", From: "master", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code"}}}}
	// An agent outside the template asking with the same question id is not
	// ours: never closed, never taken for our reminder.
	stranger := bus.Ask{ID: "ask-popped", From: "popped", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code"}}}}
	f := &fakeAsks{asks: []bus.Ask{open, stranger}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(f.defs) != 0 || len(f.withdrawn) != 1 || !strings.HasPrefix(f.withdrawn[0], "master|") {
		t.Fatalf("trusted now: defs %+v withdrawn %q", f.defs, f.withdrawn)
	}

	work, _, deps, _ = gateFixture(t, -1) // still untrusted, its ask open
	f = &fakeAsks{asks: []bus.Ask{open}}
	deps = withAsks(deps, f)
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 0 || len(f.withdrawn) != 0 {
		t.Fatalf("still untrusted: defs %+v withdrawn %q", f.defs, f.withdrawn)
	}
}

// Another agent's open ask with a trust id does not stand for ours.
func TestUpTrustAskIgnoresAStranger(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	stranger := bus.Ask{ID: "ask-popped", From: "popped", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code"}}}}
	f := &fakeAsks{asks: []bus.Ask{stranger}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || f.defs[0].From != "master" {
		t.Fatalf("defs %+v: a stranger's ask stood for master's", f.defs)
	}
}

// One untrusted agent and no other to wait for: the ask opens all the same
// (up waits on nothing, so its line alone would be the only word).
func TestUpOpensATrustAskForALoneAgent(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	deps.TabClient = func(cmd string) string {
		if strings.Contains(cmd, "agent-launch master") {
			return "claude-code"
		}
		return ""
	}
	f := &fakeAsks{}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(f.defs) != 1 || f.defs[0].From != "master" {
		t.Fatalf("defs %+v", f.defs)
	}
}

// The broker's asks failing never fails up: it says so and goes on.
func TestUpTrustAskErrorIsANote(t *testing.T) {
	work, _, deps, _ := gateFixture(t, 2)
	deps = withAsks(deps, &fakeAsks{err: errors.New("no broker")})
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String()+errw.String(), "busmon ask not opened") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}
}
