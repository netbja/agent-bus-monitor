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
		{PanelForYou, planNextPrefix + "t1", false, "task"},
		{PanelForYou, "merge:" + mqH, false, "PR"},
		{PanelForYou, mergeUnknownKey, false, rowNoPair},
		{PanelForYou, "health:team-stall:1", false, "health"},
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
		{PanelWork, "plan:t1", false, "task"},
		{PanelWork, "idea:q", false, "where"},
		{PanelWork, workFoldBetween, false, "requests"},
		{PanelWork, workFoldDone, false, "done"},
		{PanelWork, "agent:coder", false, "agent"},
		{PanelForYou, "", false, rowNoPair},
		{PanelAgents, "", false, rowNoPair},
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
	done := plan.Task{ID: "d1", Title: "lot 1", Owner: "coder"}
	s.planState = planState{loaded: true, file: plan.File{Plan: "v0.29", Goal: "triage", Tasks: []plan.Task{person, coder, done}},
		rows: []plan.Row{{Task: person, State: plan.Waiting}, {Task: coder, State: plan.Upcoming}, {Task: done, State: plan.Done}}}
	if empty {
		s.asks, s.agents, s.personGates = nil, map[string]*agentState{}, nil
		s.planState.rows[0].State = plan.Upcoming
		for _, fl := range m.feed.feed { // every line read: For you is empty
			if !fl.sep {
				m.markLineRead(fl.ev.ID)
			}
		}
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
	"where": {ovIdea}, "done": {ovPlan},
}

var inPlaceWords = map[string]bool{"expand": true, "fold": true, "unfold": true}

// reqOpenShell is the Requests fixture with every group, every older row and
// the notices line open, and the answered threads shown: the open state of
// each in-place row, and the ntc: rows the notices line holds.
func reqOpenShell(t *testing.T) Model {
	t.Helper()
	m := reqShell(t, 140, 40, nil)
	m.ui.reqOpen.folded = true
	if m.ui.reqOpen.groups == nil {
		m.ui.reqOpen.groups = map[string]bool{}
	}
	for i := 0; i < 3; i++ { // opening a group can show an older row inside it
		for _, it := range m.lists.requests.items {
			if strings.HasPrefix(it.Key, "grp:") || strings.HasPrefix(it.Key, "older:") || it.Key == reqNoticesKey {
				m.ui.reqOpen.groups[it.Key] = true
			}
		}
		m.refresh()
	}
	return m
}

// checkEnter holds the footer and Enter to word on the selected row of p in
// m: the footer opens with "enter <word>" (no enter at all for rowNoPair),
// and Enter opens the page the word names, changes the row in place, or does
// nothing. For an in-place row it returns the row's word after the change
// ("" when the row is gone, as the Plan toggle is once used).
func checkEnter(t *testing.T, m Model, p PanelID, key, what string) (Model, string, string) {
	t.Helper()
	word, known := rowHint(p, m.selectedItem(p))
	if !known {
		t.Errorf("%s: no word for this key", what)
		return m, word, ""
	}
	foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint).text()
	if word == rowNoPair {
		if strings.Contains(foot, "enter") {
			t.Errorf("%s: the footer offers enter: %q", what, foot)
		}
	} else if !strings.HasPrefix(strings.TrimSpace(foot), "enter "+word+" ") {
		t.Errorf("%s: the footer does not open with enter %s: %q", what, word, foot)
	}
	m = keys(t, m, kEnter)
	top := m.rd.top()
	after := ""
	switch {
	case word == rowNoPair:
		if top != nil {
			t.Errorf("%s: Enter opened %v", what, top.kind)
		}
	case word == "requests": // Work's between-agents line: its sub-view, no page
		if top != nil || m.ui.focus != PanelRequests {
			t.Errorf("%s: Enter left focus %v, page %v", what, m.ui.focus, top)
		}
	case inPlaceWords[word]:
		if top != nil {
			t.Errorf("%s: Enter opened %v, want a change in place", what, top.kind)
			break
		}
		if m.lists.of(p).Selected() == key {
			after, _ = rowHint(p, m.selectedItem(p))
			if after == word {
				t.Errorf("%s: nothing changed in place", what)
			}
			// The word says which way: expand and unfold open the row, fold
			// closes it (a Requests fold's open is ListItem.Expanded, whatever
			// reqOpen.folded is called).
			if open := m.selectedItem(p).Expanded; open != (word != "fold") {
				t.Errorf("%s: after %s the row is open=%v", what, word, open)
			}
		}
	default:
		if top == nil || !containsKind(enterOutcome[word], top.kind) {
			t.Errorf("%s: Enter opened %+v, want %v", what, top, enterOutcome[word])
		}
	}
	return m, word, after
}

// Every selectable row of every panel, over five fixtures: its key has a word
// (no prefix left to the binding's "detail"), the footer opens with it, and
// Enter does what the word says. An in-place row is then checked again in
// its new state (an expanded line's "full text", an open group's "fold").
// Every row kind the producers make must be met (foureyes).
func TestRowHintEveryRowAndItsOutcome(t *testing.T) {
	seen, kinds := map[string]bool{}, map[string]bool{}
	for _, fx := range []struct {
		name  string
		build func(t *testing.T) Model
	}{
		{"rich", func(t *testing.T) Model { return rowsFixture(t, false, false) }},
		{"merge unknown", func(t *testing.T) Model { return rowsFixture(t, true, false) }},
		{"empty", func(t *testing.T) Model { return rowsFixture(t, false, true) }},
		{"requests", func(t *testing.T) Model { return reqShell(t, 140, 40, nil) }},
		{"requests open", reqOpenShell},
		{"work", func(t *testing.T) Model { return workShell(t, 140, 40) }},
	} {
		base := fx.build(t)
		for _, p := range shellPanels {
			base.focusPanel(p)
			base.refresh()
			for _, it := range base.lists.of(p).items {
				if !it.Selectable || it.Key == "" {
					continue
				}
				m := fx.build(t)
				m.focusPanel(p)
				m.lists.of(p).Select(it.Key)
				m.refresh()
				what := fmt.Sprintf("%s, panel %d, %q", fx.name, p, it.Key)
				m, word, after := checkEnter(t, m, p, it.Key, what)
				seen[fmt.Sprintf("%d %s", p, word)] = true
				kinds[fmt.Sprintf("%d %s", p, keyKind(it.Key))] = true
				if after != "" {
					_, again, _ := checkEnter(t, m, p, it.Key, what+" (after the change)")
					seen[fmt.Sprintf("%d %s", p, again)] = true
				}
			}
		}
	}
	// No row selected: Enter does nothing, so the footer offers no enter.
	m := rowsFixture(t, false, false)
	m.focusPanel(PanelForYou)
	m.lists.of(PanelForYou).Select("")
	m.refresh()
	if m.lists.of(PanelForYou).Selected() == "" {
		checkEnter(t, m, PanelForYou, "", "no row selected")
		seen["no selection"] = true
	}
	var got []string
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"no selection"}
	for p, words := range map[PanelID][]string{
		PanelForYou:   {"answer", "agent", "thread", "task", "PR", rowNoPair, "health"},
		PanelAgents:   {"agent"},
		PanelActivity: {"expand", "full text"},
		PanelBoard:    {"task"},
		PanelRequests: {"answer", "request", "thread", "notices", "fold", "unfold"},
		PanelWork:     {"answer", "agent", "thread", "task", "PR", "health", "request", "where", "requests", "done"},
	} {
		for _, w := range words {
			want = append(want, fmt.Sprintf("%d %s", p, w))
		}
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("the fixtures never met %q; met: %q", w, got)
		}
	}
	// Each row-key kind of each producer, so a word shared by two kinds
	// (task: and plan-next:) cannot hide a kind never met. For you has no
	// plan: or unread: rows any more (home-inbox-drawer §3). The
	// Requests "asks" key has no producer: openRequestRow alone knows it.
	for p, ks := range map[PanelID][]string{
		PanelForYou:   {"ask", "wait", "prompt", "gate", "plan-next", "merge", mergeUnknownKey, "health"},
		PanelRequests: {"ask", "req", "thr", "ntc", reqNoticesKey, "fold", "grp", "older"},
		PanelWork:     {"ask", "wait", "prompt", "gate", "merge", "health", "plan", "agent", "idea", "req", "thr", workFoldBetween, workFoldDone},
	} {
		for _, k := range ks {
			if !kinds[fmt.Sprintf("%d %s", p, k)] {
				t.Errorf("the fixtures never met panel %d's %q rows", p, k)
			}
		}
	}
}

// keyKind is a row key's kind: its prefix ("ask", "plan-next"), or the key
// itself for a fixed one (merge:unknown, ntc:all, fold, toggle).
func keyKind(key string) string {
	switch {
	case key == mergeUnknownKey, key == reqNoticesKey, !strings.Contains(key, ":"):
		return key
	}
	return key[:strings.Index(key, ":")]
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
