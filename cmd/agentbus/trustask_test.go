package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// fakeAsks is the broker's asks for up's trust reminder (first-run-trust (b)).
type fakeAsks struct {
	asks      []bus.Ask
	defs      []bus.AskDef
	withdrawn []string // agent|id|qid|note
	err       error
}

// Ask behaves as the bus does: one open ask per agent (ASK_OPEN), and a
// replace that supersedes the agent's open ask.
func (f *fakeAsks) Ask(_ context.Context, def bus.AskDef, replaces string) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	if err := bus.ValidateAskDef(def); err != nil {
		return "", "", err
	}
	for i, a := range f.asks {
		if a.From != def.From || a.State != "open" {
			continue
		}
		if a.ID != replaces {
			return "", "", fmt.Errorf("%w: this agent already has an open ask", bus.ErrAskRefused)
		}
		f.asks[i].State = "replaced"
	}
	f.defs = append(f.defs, def)
	id := fmt.Sprintf("ask-%s-%d", def.From, len(f.defs))
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

// An untrusted gated client gets a reminder in For you, from agentbus (never
// from an agent: its ask slot stays free): the folder, where to answer, the
// choice by its label and what trusting loads; it closes itself once the
// trust is accepted during up's wait. A trusted client gets none. Nothing is
// written to a client's file.
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
	if d.From != "agentbus" || q.ID != "trust-claude-code" || q.Header != "Folder trust" || bus.ValidateAskDef(d) != nil ||
		!strings.Contains(q.Question, `choose "Yes, I trust this folder"`) || !strings.Contains(q.Question, "master's pane") ||
		!strings.Contains(q.Context, work) || !strings.Contains(q.Context, "hooks") {
		t.Fatalf("ask %+v (valid: %v)", d, bus.ValidateAskDef(d))
	}
	if len(f.withdrawn) != 1 || !strings.HasPrefix(f.withdrawn[0], "agentbus|ask-agentbus-1|trust-claude-code|") || !strings.Contains(f.withdrawn[0], "trusted") {
		t.Fatalf("withdrawn %q", f.withdrawn)
	}
}

// foureyes: the gate agent keeps its one ask slot: while the reminder is
// open it can still ask the person, and its own open ask is left untouched.
func TestUpTrustAskLeavesTheAgentsAskSlot(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	mine := bus.AskQuestion{ID: "lot", Header: "Lot 1", Question: "review lot 1?", Options: []bus.AskOption{{Label: "yes"}}}
	coders := bus.Ask{ID: "ask-coder-0", From: "coder", Title: "Lot 1", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: mine}}}
	f := &fakeAsks{asks: []bus.Ask{coders}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || f.defs[0].From != "agentbus" || f.asks[0].State != "open" {
		t.Fatalf("defs %+v asks %+v", f.defs, f.asks)
	}
	if _, _, err := f.Ask(context.Background(), bus.AskDef{From: "master", Title: "Merge?", Questions: []bus.AskQuestion{mine}}, ""); err != nil {
		t.Fatalf("master cannot ask while the reminder is open: %v", err)
	}
}

// Two untrusted clients: one agentbus ask, a question each (the second
// joins through a replace, the first kept); both close once trusted.
func TestUpTrustAskTwoClientsOneAsk(t *testing.T) {
	work, _, deps, polls := gateFixture(t, 2)
	trustedAfter := deps.Trusted
	deps.Trusted = func(client, dir string) bool {
		if client == "codex" {
			return *polls >= 2
		}
		return trustedAfter(client, dir)
	}
	f := &fakeAsks{}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(f.defs) != 2 || len(f.defs[1].Questions) != 2 || f.defs[1].Questions[0].ID != "trust-claude-code" || f.defs[1].Questions[1].ID != "trust-codex" {
		t.Fatalf("defs %+v", f.defs)
	}
	if len(f.withdrawn) != 2 {
		t.Fatalf("withdrawn %q", f.withdrawn)
	}
	for _, w := range f.withdrawn {
		if !strings.HasPrefix(w, "agentbus|ask-agentbus-2|") {
			t.Errorf("withdrawn from the replaced ask: %q", w)
		}
	}
}

// Not accepted in time: the reminder stays open for the person.
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

func trustQuestion(client, header string) bus.AskQuestionState {
	return bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: "trust-" + client, Header: header, Question: "x?", Options: []bus.AskOption{{Label: "a"}}}}
}

// A later up closes a reminder whose folder is now trusted, and opens no
// second one while it is still open. A question from anyone but agentbus,
// or not headed Folder trust, is not a reminder.
func TestUpSettlesItsTrustAsks(t *testing.T) {
	work, _, deps, _ := gateFixture(t, 0) // trusted from the start
	open := bus.Ask{ID: "ask-agentbus-0", From: "agentbus", State: "open", Questions: []bus.AskQuestionState{trustQuestion("claude-code", "Folder trust")}}
	stranger := bus.Ask{ID: "ask-coder-0", From: "coder", State: "open", Questions: []bus.AskQuestionState{trustQuestion("claude-code", "Folder trust")}}
	f := &fakeAsks{asks: []bus.Ask{open, stranger}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(f.defs) != 0 || len(f.withdrawn) != 1 || !strings.HasPrefix(f.withdrawn[0], "agentbus|ask-agentbus-0|") {
		t.Fatalf("trusted now: defs %+v withdrawn %q", f.defs, f.withdrawn)
	}

	work, _, deps, _ = gateFixture(t, -1) // still untrusted, its reminder open
	open.State = "open"
	f = &fakeAsks{asks: []bus.Ask{open}}
	deps = withAsks(deps, f)
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 0 || len(f.withdrawn) != 0 {
		t.Fatalf("still untrusted: defs %+v withdrawn %q", f.defs, f.withdrawn)
	}

	work, _, deps, _ = gateFixture(t, -1) // another sender's question with the same id stands for nothing
	f = &fakeAsks{asks: []bus.Ask{stranger}}
	deps = withAsks(deps, f)
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || f.defs[0].From != "agentbus" || len(f.withdrawn) != 0 {
		t.Fatalf("stranger: defs %+v withdrawn %q", f.defs, f.withdrawn)
	}
}

// agentbus's open ask with no room (full, or the id taken by a question that
// is not a reminder): a note, never a replace the bus would refuse.
func TestUpTrustAskNoRoomIsANote(t *testing.T) {
	full := bus.Ask{ID: "ask-agentbus-0", From: "agentbus", Title: "T", State: "open"}
	for i := range bus.AskMaxQuestions {
		full.Questions = append(full.Questions, bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: fmt.Sprintf("q%d", i), Header: "H", Question: "x?", Options: []bus.AskOption{{Label: "a"}}}})
	}
	taken := bus.Ask{ID: "ask-agentbus-0", From: "agentbus", Title: "T", State: "open", Questions: []bus.AskQuestionState{trustQuestion("claude-code", "Other")}}
	for _, a := range []bus.Ask{full, taken} {
		work, _, deps, _ := gateFixture(t, -1)
		f := &fakeAsks{asks: []bus.Ask{a}}
		deps = withAsks(deps, f)
		var out, errw bytes.Buffer
		runUp([]string{"demo", work}, deps, &out, &errw)
		if len(f.defs) != 0 || !strings.Contains(out.String(), "no room") {
			t.Errorf("%d questions: defs %+v out %q", len(a.Questions), f.defs, out.String())
		}
	}
}

// One untrusted agent and no other to wait for: the reminder opens all the
// same (up waits on nothing, so its line alone would be the only word).
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
	if len(f.defs) != 1 || !strings.Contains(f.defs[0].Questions[0].Question, "master's pane") {
		t.Fatalf("defs %+v", f.defs)
	}
}

// The broker failing never fails up: a note when there is a dialog to
// answer, nothing when there is none (foureyes).
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
	work, _, deps, _ = gateFixture(t, 0) // trusted: no dialog
	deps = withAsks(deps, &fakeAsks{err: errors.New("no broker")})
	out.Reset()
	runUp([]string{"demo", work}, deps, &out, &errw)
	if strings.Contains(out.String(), "busmon ask") || strings.Contains(out.String(), "dialog") {
		t.Fatalf("a note with no dialog: %q", out.String())
	}
}

// Against the real bus (testredis): master's own open ask is untouched and it
// keeps its slot; the reminder is agentbus's; once trusted, it is withdrawn.
func TestTrustRemindersOnTheRealBus(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "tr" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	t.Cleanup(func() { c.Del(ctx, bus.AsksKey(project), bus.StreamKey(project, "cmd")) })
	q := bus.AskQuestion{ID: "one", Header: "Lot", Question: "one?", Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}
	masters, _, err := b.Ask(ctx, bus.AskDef{From: "master", Title: "Lot 1", Questions: []bus.AskQuestion{q}}, "")
	if err != nil {
		t.Fatal(err)
	}
	trusted := false
	d := upDeps{Out: io.Discard, TrustAsks: func(string) (trustAskBus, error) { return b, nil },
		Trusted: func(string, string) bool { return trusted }}
	tpl := upTemplate{Name: project, Tabs: []upTab{{Name: "master", Command: "agent-launch master"}}}
	settle := d.trustReminders(tpl, t.TempDir(), map[string]string{"claude-code": "master"}, map[string]string{"master": "w1:p2"})
	asks, _ := b.Asks(ctx)
	var reminder string
	for _, a := range asks {
		switch {
		case a.ID == masters && a.State != "open":
			t.Fatalf("master's ask touched: %+v", a)
		case a.From == "agentbus" && a.State == "open" && len(a.Questions) == 1 && a.Questions[0].ID == "trust-claude-code":
			reminder = a.ID
		}
	}
	if reminder == "" {
		t.Fatalf("no reminder from agentbus in %+v", asks)
	}
	trusted = true
	settle()
	asks, _ = b.Asks(ctx)
	for _, a := range asks {
		if a.ID == reminder && (a.Questions[0].Answer == nil || !a.Questions[0].Answer.Withdrawn) {
			t.Fatalf("reminder not withdrawn: %+v", a)
		}
		if a.ID == masters && a.State != "open" {
			t.Fatalf("master's ask closed: %+v", a)
		}
	}
}
