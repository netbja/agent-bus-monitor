package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func demoAsk() bus.Ask {
	return bus.Ask{ID: "100-0", From: "master", Title: "Opening of act 4", State: "open", Questions: []bus.AskQuestionState{
		{AskQuestion: bus.AskQuestion{ID: "amend", Header: "Amendment", Question: "Draft the amendment?", Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}},
			Answer: &bus.AskAnswer{Choice: "Yes", By: "charlie"}},
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
	threads := []openThread{{ID: "1", Target: "charlie"}, {ID: "2", Target: "foureyes"}, {ID: "3", Target: "coder"}}
	if got := requestsLabel(nil, threads, "charlie", 3).text(); got != "4 for you · 2 between agents" {
		t.Fatalf("label %q", got)
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
