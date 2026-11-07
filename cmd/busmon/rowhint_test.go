package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

// busmon-row-actions (busmon-queue-triage §5.1): the word for Enter, row key
// by row key: the page Enter opens, the change it makes in place, or no pair.
func TestRowHintTable(t *testing.T) {
	for _, c := range []struct {
		p        PanelID
		key      string
		expanded bool
		want     string
	}{
		{PanelForYou, "ask:1-0", false, "answer"},
		{PanelForYou, "wait:coder:1", false, "agent"},
		{PanelForYou, "prompt:coder:1", false, "agent"},
		{PanelForYou, "gate:coder:1-0", false, "thread"},
		{PanelForYou, "plan:t1", false, "task"},
		{PanelForYou, "empty:plan:t1", false, "task"},
		{PanelForYou, "merge:" + mqH, false, "PR"},
		{PanelForYou, mergeUnknownKey, false, rowNoPair},
		{PanelForYou, "health:team-stall:1", false, "health"},
		{PanelForYou, "unread:1-0", false, "expand"},
		{PanelForYou, "unread:1-0", true, "full text"},
		{PanelActivity, "1-0", false, "expand"},
		{PanelActivity, "1-0", true, "full text"},
		{PanelRequests, "asks", false, "answer"},
		{PanelRequests, "ask:1-0", false, "answer"},
		{PanelRequests, "req:t1", false, "request"},
		{PanelRequests, "thr:1-0", false, "thread"},
		{PanelRequests, "ntc:grp:1|a|b|report", false, "notices"},
		{PanelRequests, reqNoticesKey, false, "unfold"},
		{PanelRequests, reqNoticesKey, true, "fold"},
		{PanelRequests, "fold", false, "unfold"},
		{PanelRequests, "fold", true, "fold"},
		{PanelRequests, "grp:1|a|b|report", false, "unfold"},
		{PanelRequests, "older:grp:1|a|b|report", true, "fold"},
		{PanelAgents, "coder", false, "agent"},
		{PanelBoard, "busmon-charm-b", false, "task"},
		{PanelPlan, "task:t1", false, "task"},
		{PanelPlan, "toggle", false, "expand"},
	} {
		got, known := rowHint(c.p, ListItem{Key: c.key, Expanded: c.expanded})
		if got != c.want || !known {
			t.Errorf("panel %d %q expanded=%v: %q (known %v), want %q", c.p, c.key, c.expanded, got, known, c.want)
		}
	}
	if w, known := rowHint(PanelForYou, ListItem{Key: "new-kind:1"}); w != "" || known {
		t.Errorf("an unknown key: %q %v, want the binding's own word", w, known)
	}
}

// rowsFixture is the shell's fixture with every For you kind besides its
// own (a pane prompt, the stall guard, a plan task, a merge) and every
// panel's rows; unknownMerge swaps the merge for merge:unknown, empty leaves
// For you with only its empty state and the next decision.
func rowsFixture(t *testing.T, unknownMerge, empty bool) Model {
	t.Helper()
	m := shellWith(t, 140, 40, shellConfig{}, false)
	s := shellFixture(shellNow)
	s.pause = nil
	for _, a := range s.agents {
		a.judgedAt = shellNow
	}
	person := plan.Task{ID: "p1", Title: "say yes to lot 2", Owner: plan.Person}
	coder := plan.Task{ID: "c1", Title: "build lot 2", Owner: "coder"}
	s.planState = planState{loaded: true, file: plan.File{Plan: "v0.29", Goal: "triage", Tasks: []plan.Task{person, coder}},
		rows: []plan.Row{{Task: person, State: plan.Waiting}, {Task: coder, State: plan.Upcoming}}}
	if empty {
		s.asks, s.agents, s.personGates = nil, map[string]*agentState{}, nil
		s.planState.rows[0].State = plan.Upcoming
		return step(t, m, pollMsg{snap: s, at: shellNow})
	}
	s.agents["scout"] = &agentState{state: "idle", stateAt: shellNow.Add(-time.Minute), lastSeen: shellNow, registered: true,
		obsPrompt: true, obsAt: shellNow, obsSince: shellNow.Add(-time.Minute), judgedAt: shellNow}
	s.guardianNow = shellNow
	s.guardianHealth = map[string]guardian.Record{"team-stall": {ID: "team-stall", Status: guardian.Warn, Reason: "waiting-person",
		Detail: "team idle 18m; waiting for you", ObservedAt: shellNow.UnixMilli(), Since: shellNow.Add(-3 * time.Minute).UnixMilli()}}
	s.mergeQ, s.mergeQRead = mqRecord(shellNow), true
	if unknownMerge {
		s.mergeQ = bus.MergeQueueRecord{AttemptedAt: shellNow.UnixMilli(), Reason: "no origin remote"}
	}
	return step(t, m, pollMsg{snap: s, at: shellNow})
}

// enterOutcome is what the word promises Enter does: a page kind on top of
// the reader, a change in place, or nothing.
var enterOutcome = map[string][]overlayKind{
	"answer": {ovAsk}, "agent": {ovAgent}, "thread": {ovMessage}, "task": {ovPlanTask, ovTask}, "PR": {ovMerge},
	"health": {ovHealth}, "request": {ovRequest}, "notices": {ovNotices}, "full text": {ovMessage},
}

var inPlaceWords = map[string]bool{"expand": true, "fold": true, "unfold": true}

// Every selectable row of every panel, over the fixtures: its key has a word
// (no prefix left to the binding's "detail"), and Enter does what the word
// says. The For you kinds and the Requests and Plan rows the fixtures make
// must all be met.
func TestRowHintEveryRowAndItsOutcome(t *testing.T) {
	seen := map[string]bool{}
	for _, fx := range []struct {
		name                string
		unknownMerge, empty bool
	}{{"rich", false, false}, {"merge unknown", true, false}, {"empty", false, true}} {
		base := rowsFixture(t, fx.unknownMerge, fx.empty)
		for _, p := range []PanelID{PanelForYou, PanelAgents, PanelActivity, PanelBoard, PanelRequests, PanelPlan} {
			base.focusPanel(p)
			base.refresh()
			for _, it := range base.lists.of(p).items {
				if !it.Selectable || it.Key == "" {
					continue
				}
				m := rowsFixture(t, fx.unknownMerge, fx.empty)
				m.focusPanel(p)
				m.lists.of(p).Select(it.Key)
				m.refresh()
				word, known := rowHint(p, m.selectedItem(p))
				what := fmt.Sprintf("%s, panel %d, %q (%q)", fx.name, p, it.Key, word)
				if !known {
					t.Errorf("%s: no word for this key", what)
					continue
				}
				seen[fmt.Sprintf("%d %s", p, word)] = true
				foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.shellHint).text()
				if word == rowNoPair {
					if strings.Contains(foot, "enter") {
						t.Errorf("%s: the footer offers enter: %q", what, foot)
					}
				} else if !strings.HasPrefix(strings.TrimSpace(foot), "enter "+word) {
					t.Errorf("%s: the footer does not open with enter %s: %q", what, word, foot)
				}
				m = keys(t, m, kEnter)
				top := m.rd.top()
				switch {
				case word == rowNoPair:
					if top != nil {
						t.Errorf("%s: Enter opened %v", what, top.kind)
					}
				case inPlaceWords[word]:
					if top != nil {
						t.Errorf("%s: Enter opened %v, want a change in place", what, top.kind)
					}
					after, _ := rowHint(p, m.selectedItem(p))
					if m.lists.of(p).Selected() == it.Key && after == word {
						t.Errorf("%s: nothing changed in place", what)
					}
				default:
					if top == nil || !containsKind(enterOutcome[word], top.kind) {
						t.Errorf("%s: Enter opened %+v, want %v", what, top, enterOutcome[word])
					}
				}
			}
		}
	}
	var got []string
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	for _, want := range []string{
		fmt.Sprintf("%d answer", PanelForYou), fmt.Sprintf("%d agent", PanelForYou), fmt.Sprintf("%d thread", PanelForYou),
		fmt.Sprintf("%d task", PanelForYou), fmt.Sprintf("%d PR", PanelForYou), fmt.Sprintf("%d %s", PanelForYou, rowNoPair),
		fmt.Sprintf("%d health", PanelForYou), fmt.Sprintf("%d expand", PanelForYou),
		fmt.Sprintf("%d agent", PanelAgents), fmt.Sprintf("%d expand", PanelActivity), fmt.Sprintf("%d task", PanelBoard),
		fmt.Sprintf("%d task", PanelPlan),
	} {
		if !seen[want] {
			t.Errorf("the fixtures never met %q; met: %q", want, got)
		}
	}
}

func containsKind(ks []overlayKind, k overlayKind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// Off the main screen the row's word is never used: a text mode or a page
// keeps its own words (the picker's open, a page's).
func TestShellHintOnlyOnTheMainScreen(t *testing.T) {
	m := rowsFixture(t, false, false)
	m.focusPanel(PanelAgents)
	m.refresh()
	if w := m.shellHint(ActOpen); w != "agent" {
		t.Fatalf("main screen: %q", w)
	}
	m.ui.line = lineMessage
	if w := m.shellHint(ActOpen); w != "" {
		t.Fatalf("the message line open: %q", w)
	}
	m.ui.line = lineNone
	m = keys(t, m, kEnter)
	if m.rd.top() == nil || m.pageHint(ActOpen) != "" {
		t.Fatalf("on a page: %q", m.pageHint(ActOpen))
	}
}
