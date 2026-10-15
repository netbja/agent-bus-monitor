package main

import (
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
	got := tagRE.ReplaceAllString(askPanelBody(demoAsk(), askPanel{q: 1, opt: 0}, 70), "")
	for _, want := range []string{GlyphDone + " Amendment", GlyphWorking + " Date T", GlyphIdle + " Code",
		"When should it open?", "Why", GlyphNone + " act approved", GlyphSelect + " 1. Mon 10:00 (recommended)",
		"before the DCA slice", "2. Wed 10:00", "3. Other... (type your answer)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	answered := tagRE.ReplaceAllString(askPanelBody(demoAsk(), askPanel{q: 0}, 70), "")
	if !strings.Contains(answered, "answered: Yes") || strings.Contains(answered, "Other...") {
		t.Fatalf("an answered question must show its answer only:\n%s", answered)
	}
	narrow := tagRE.ReplaceAllString(askPanelBody(demoAsk(), askPanel{q: 2}, 30), "")
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
	if !h.u.ask.typing {
		t.Fatal("Other did not start typing")
	}
	h.typeText("ship dev")
	h.key(key(tcell.KeyEnter))
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
