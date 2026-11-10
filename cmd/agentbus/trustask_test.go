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
	if len(f.withdrawn) != 1 || !strings.HasPrefix(f.withdrawn[0], "master|ask-master-1|trust-claude-code|") || !strings.Contains(f.withdrawn[0], "trusted") {
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
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code", Header: "Folder trust"}}}}
	// An agent outside the template asking with the same question id is not
	// ours: never closed, never taken for our reminder.
	stranger := bus.Ask{ID: "ask-popped", From: "popped", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code", Header: "Folder trust"}}}}
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
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code", Header: "Folder trust"}}}}
	f := &fakeAsks{asks: []bus.Ask{stranger}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || f.defs[0].From != "master" {
		t.Fatalf("defs %+v: a stranger's ask stood for master's", f.defs)
	}
}

// reviewer-codex: another agent of the same template with the same question
// id and header is not the reminder (only the client's gate agent carries
// it): it neither stands for it nor is closed as trusted.
func TestUpTrustAskIgnoresAnotherTemplateAgent(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	coders := bus.Ask{ID: "ask-coder", From: "coder", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "trust-claude-code", Header: "Folder trust"}}}}
	f := &fakeAsks{asks: []bus.Ask{coders}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 || f.defs[0].From != "master" || len(f.withdrawn) != 0 {
		t.Fatalf("defs %+v withdrawn %q", f.defs, f.withdrawn)
	}
}

// reviewer-codex: the gate agent already waits on a decision of its own; the
// reminder joins that ask as one more question (a replace), every question
// of the agent kept, instead of being refused.
func TestUpTrustAskJoinsTheGateAgentsOpenAsk(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	mine := bus.AskQuestion{ID: "lot", Header: "Lot 1", Question: "review lot 1?", Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}
	masters := bus.Ask{ID: "ask-master-0", From: "master", Title: "Lot 1", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: mine}}}
	f := &fakeAsks{asks: []bus.Ask{masters}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 1 {
		t.Fatalf("defs %+v, out %q", f.defs, out.String())
	}
	d := f.defs[0]
	if d.From != "master" || d.Title != "Lot 1" || len(d.Questions) != 2 || d.Questions[0].ID != "lot" || d.Questions[1].ID != "trust-claude-code" {
		t.Fatalf("merged %+v", d)
	}
	if strings.Contains(out.String(), "busmon ask not opened") {
		t.Fatalf("refused: %q", out.String())
	}
}

// The gate agent's own question that only shares the id is not the
// reminder: its header tells them apart. The reminder cannot join that ask
// (the id is taken: the bus would refuse the whole ask), so up says so.
func TestUpTrustAskNeedsItsHeader(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	own := bus.AskQuestion{ID: "trust-claude-code", Header: "My own", Question: "x?", Options: []bus.AskOption{{Label: "a"}}}
	f := &fakeAsks{asks: []bus.Ask{{ID: "ask-master-0", From: "master", Title: "T", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: own}}}}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 0 || len(f.withdrawn) != 0 || !strings.Contains(out.String(), "already uses the id trust-claude-code") {
		t.Fatalf("defs %+v withdrawn %q out %q", f.defs, f.withdrawn, out.String())
	}
}

// A gate agent whose open ask is full (bus.AskMaxQuestions): no replace that
// would fail, a note instead.
func TestUpTrustAskFullAskIsANote(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	full := bus.Ask{ID: "ask-master-0", From: "master", Title: "T", State: "open"}
	for i := range bus.AskMaxQuestions {
		full.Questions = append(full.Questions, bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: fmt.Sprintf("q%d", i), Header: "H", Question: "x?", Options: []bus.AskOption{{Label: "a"}}}})
	}
	f := &fakeAsks{asks: []bus.Ask{full}}
	deps = withAsks(deps, f)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if len(f.defs) != 0 || !strings.Contains(out.String(), "already has 6 questions") {
		t.Fatalf("defs %+v out %q", f.defs, out.String())
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

// Against the real bus (testredis): the reminder joins master's open ask
// through a replace, master's answered question keeps its answer and its
// open one stays open; once trusted, only the reminder is withdrawn.
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
	q := func(id string) bus.AskQuestion {
		return bus.AskQuestion{ID: id, Header: "Lot", Question: id + "?", Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}
	}
	id, _, err := b.Ask(ctx, bus.AskDef{From: "master", Title: "Lot 1", Questions: []bus.AskQuestion{q("one"), q("two")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, id, "one", "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	trusted := false
	d := upDeps{Out: io.Discard, TrustAsks: func(string) (trustAskBus, error) { return b, nil },
		Trusted:   func(string, string) bool { return trusted },
		TabClient: func(string) string { return "claude-code" }}
	tpl := upTemplate{Name: project, Tabs: []upTab{{Name: "master", Command: "agent-launch master"}}}
	settle := d.trustReminders(tpl, t.TempDir(), map[string]string{"claude-code": "master"}, map[string]string{"master": "w1:p2"})
	asks, _ := b.Asks(ctx)
	var cur bus.Ask
	for _, a := range asks {
		if a.From == "master" && a.State == "open" {
			cur = a
		}
	}
	if len(cur.Questions) != 3 || cur.Questions[0].Answer == nil || cur.Questions[0].Answer.Choice != "yes" || cur.Questions[1].Answer != nil || cur.Questions[2].ID != "trust-claude-code" {
		t.Fatalf("merged ask %+v", cur)
	}
	trusted = true
	settle()
	asks, _ = b.Asks(ctx)
	for _, a := range asks {
		if a.ID != cur.ID {
			continue
		}
		if a.State != "open" || a.Questions[2].Answer == nil || !a.Questions[2].Answer.Withdrawn || a.Questions[1].Answer != nil {
			t.Fatalf("after trust %+v", a)
		}
	}
}
