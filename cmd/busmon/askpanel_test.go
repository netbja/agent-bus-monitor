package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

func demoAsk() bus.Ask {
	return bus.Ask{ID: "100-0", From: "master", Title: "Opening of act 4", State: "open", Questions: []bus.AskQuestionState{
		{AskQuestion: bus.AskQuestion{ID: "amend", Header: "Amendment", Question: "Draft the amendment?", Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}},
			Answer: &bus.AskAnswer{Choice: "Yes", By: "bernard"}},
		{AskQuestion: bus.AskQuestion{ID: "date", Header: "Date T", Question: "When should it open?",
			Context: "## Why\n- act approved\n- 48 h rule",
			Options: []bus.AskOption{{Label: "Mon 10:00", Description: "before the DCA slice", Recommended: true}, {Label: "Wed 10:00"}}}},
		{AskQuestion: bus.AskQuestion{ID: "sha", Header: "Code", Question: "Which code?", Options: []bus.AskOption{{Label: "X"}, {Label: "Y"}}}},
	}}
}

func TestAskPanelBody(t *testing.T) {
	got := tagRE.ReplaceAllString(plainOf(askPanelBody(demoAsk(), askPanel{q: 1, opt: 0}, 70)), "")
	for _, want := range []string{GlyphDone + " Amendment", GlyphWorking + " Date T", GlyphIdle + " Code",
		"When should it open?", "Why", GlyphNone + " act approved", GlyphSelect + " 1. Mon 10:00 (recommended)",
		"before the DCA slice", "2. Wed 10:00", "3. Other... (type your answer)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	answered := tagRE.ReplaceAllString(plainOf(askPanelBody(demoAsk(), askPanel{q: 0}, 70)), "")
	if !strings.Contains(answered, "answered: Yes") || strings.Contains(answered, "Other...") {
		t.Fatalf("an answered question must show its answer only:\n%s", answered)
	}
	narrow := tagRE.ReplaceAllString(plainOf(askPanelBody(demoAsk(), askPanel{q: 2}, 30)), "")
	if !strings.Contains(narrow, "(3/3") {
		t.Fatalf("narrow tab strip:\n%s", narrow)
	}
}

func askShared(now time.Time) *shared {
	st := demoShared(now)
	st.asks = []bus.Ask{demoAsk()}
	st.agents["master"] = &agentState{state: "working", stateAt: now, registered: true, openAsk: 2, askID: "100-0"}
	return st
}

func TestAskPanelSendsEachAnswerAtOnce(t *testing.T) {
	h := newHarness(t, askShared(time.Now()))
	h.draw(160, 45)
	if waitingQuestions(h.u.st.agents) != 2 {
		t.Fatalf("header count %d, want the 2 open questions", waitingQuestions(h.u.st.agents))
	}
	h.key(runeKey('!'))
	if h.u.ov != ovAsk || h.u.ask.q != 1 || h.u.ask.opt != 0 {
		t.Fatalf("panel: ov %v cursor %+v (want the first open question, recommended option)", h.u.ov, h.u.ask)
	}
	h.w.left = 1
	h.key(runeKey('2'))
	h.key(key(tcell.KeyEnter))
	if got := strings.Join(h.w.sent, "|"); got != "answer 100-0 date Wed 10:00" {
		t.Fatalf("sent %q", got)
	}
	if h.u.ov != ovAsk || h.u.ask.q != 2 {
		t.Fatalf("after the answer: ov %v q %d (want the next open question)", h.u.ov, h.u.ask.q)
	}
	h.key(runeKey('?'))
	if h.u.ov != ovHelp {
		t.Fatal("? must open the help from the ask panel")
	}
	h.key(key(tcell.KeyEscape))
	h.key(runeKey('!'))
	h.u.ask.q = 2
	h.w.left = 0
	h.key(runeKey('3')) // Other...
	h.key(key(tcell.KeyEnter))
	if h.u.mode() != ModeCompose || h.u.comp.kind != composeAnswer {
		t.Fatal("Other did not open the compose window for an answer")
	}
	h.typeText("ship dev")
	h.key(key(tcell.KeyCtrlS))
	if got := h.w.sent[len(h.w.sent)-1]; got != "answer 100-0 sha ship dev" {
		t.Fatalf("free text sent %q", got)
	}
	if h.u.ov != ovNone {
		t.Fatal("the last answer must close the panel")
	}
}

func TestAskRowFadesWhateverTheAgentDeclared(t *testing.T) {
	now := time.Now()
	st := askShared(now)
	row := agentRow("master", st.agents["master"], false, TierWide, 120, now)
	found := false
	for _, s := range row {
		found = found || (strings.Contains(s.Text, "waiting") && s.Fade > 0)
	}
	if !found {
		t.Fatalf("an agent with open questions must read waiting (fading): %+v", row)
	}
}

func TestRequestsLabelAddsQuestionsOnce(t *testing.T) {
	threads := []openThread{{ID: "1", Target: "bernard"}, {ID: "2", Target: "foureyes"}, {ID: "3", Target: "coder"}}
	if got := requestsLabel(nil, threads, "bernard", 3).text(); got != "4 for you · 2 between agents" {
		t.Fatalf("label %q", got)
	}
}

func TestAskPanelStaysOnARefusal(t *testing.T) {
	h := newHarness(t, askShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('!'))
	h.w.fail = errors.New("ask refused: this question is already answered")
	h.w.asks = []bus.Ask{demoAsk()}
	h.key(key(tcell.KeyEnter))
	if h.u.ov != ovAsk || !strings.Contains(h.u.flash, "already answered") {
		t.Fatalf("panel %v flash %q: a refusal must keep the panel and say why", h.u.ov, h.u.flash)
	}
}

func TestAskPanelFollowsAReplacement(t *testing.T) {
	now := time.Now()
	st := askShared(now)
	old := demoAsk()
	h := newHarness(t, st)
	h.draw(160, 45)
	h.key(runeKey('!'))
	repl := demoAsk()
	repl.ID = "200-0"
	repl.Questions[1].Answer = &bus.AskAnswer{Choice: "Mon 10:00", By: "bernard"}
	old.State, old.ReplacedBy = "replaced", "200-0"
	h.w.asks = []bus.Ask{old, repl}
	h.w.answerID, h.w.left = "200-0", 1
	h.key(key(tcell.KeyEnter))
	if h.u.ov != ovAsk || h.u.ask.id != "200-0" || h.u.ask.q != 2 {
		t.Fatalf("panel %v on %s q %d: must follow the replacement to its open question", h.u.ov, h.u.ask.id, h.u.ask.q)
	}
}

func TestBellRingsOncePerAskNotOnItsCompletion(t *testing.T) {
	now := time.Now()
	h := newHarness(t, demoShared(now))
	rings := 0
	h.u.bell = func() { rings++ }
	h.u.st.agents["master"] = &agentState{state: "waiting", stateAt: now}
	h.u.onTick() // a plain waiting declaration
	h.u.st.agents["master"].askID, h.u.st.agents["master"].openAsk = "100-0", 2
	h.u.onTick() // the ask
	h.u.onTick()
	h.u.st.agents["master"].askID, h.u.st.agents["master"].openAsk = "", 0
	h.u.onTick() // answered: the same waiting declaration again
	if rings != 2 {
		t.Fatalf("rings %d, want 2 (the waiting, the ask; not the completion)", rings)
	}
}

func TestActivityAskRowReadsTheHash(t *testing.T) {
	currentAsks = map[string]bus.Ask{"100-0": demoAsk()}
	t.Cleanup(func() { currentAsks = map[string]bus.Ask{} })
	look := lookEvent(bus.Event{ID: "100-0", Kind: "cmd", Type: bus.CmdAsk, From: "master", Target: bus.PeopleTarget, Message: "Opening of act 4"})
	if !strings.Contains(look.Text, "(3 question(s), 2 open)") || look.To != "" {
		t.Fatalf("look %+v", look)
	}
}

func TestCopiesAreSanitised(t *testing.T) {
	if got := agentDetailPlain(&agentState{message: "ok\x1b[31m‮evil"}); strings.ContainsAny(got, "\x1b‮") {
		t.Fatalf("copy kept a control: %q", got)
	}
}

func TestYCopiesTheQuestion(t *testing.T) {
	h := newHarness(t, askShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('!'))
	if !strings.Contains(h.u.ovPlain, "When should it open?") || !strings.Contains(h.u.ovPlain, "1. Mon 10:00") {
		t.Fatalf("copy buffer %q", h.u.ovPlain)
	}
	h.key(runeKey('y'))
	if h.u.ov != ovAsk || !strings.Contains(h.u.flash, "question copied") {
		t.Fatalf("y: panel %v flash %q", h.u.ov, h.u.flash)
	}
}
