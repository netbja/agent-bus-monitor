package main

import (
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-unread §4's guard: foldLines records, in Held, every line it folds
// into an item: the item's own ids, an ask's own line, a waiting agent's
// newest report, a --needs line naming one live target. A report without
// --needs that only attaches to the items it names is held by none.
func TestForYouHeld(t *testing.T) {
	items := func(s snapshot, f *activityFeed) []attentionItem {
		f.classifyWaits(waitingNames(s.agents))
		marks := f.unread.marks(f.feed, nil)
		for i := range f.feed {
			f.feed[i].mark = marks[f.feed[i].ev.ID]
		}
		return forYou(s, f.feed)
	}
	held := func(its []attentionItem, id string) bool {
		for _, it := range its {
			if slices.Contains(it.Held, id) {
				return true
			}
		}
		return false
	}

	// A --needs read report: its own item (IDs), its line held.
	s := snapshot{person: "bob", agents: map[string]*agentState{}}
	f := newFoldFeed()
	rep := repEv(0, "master", "## Done")
	rep.Needs = bus.NeedsRead
	f.add(rep, "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	if its := items(s, f); !held(its, rid(0)) || !held(its, rid(1)) {
		t.Errorf("a --needs read report item does not hold its line: %+v", its)
	}

	// An ask's own line.
	f = newFoldFeed()
	ask := bus.Event{Kind: "cmd", ID: rid(5), From: "coder", Target: "bob", Type: bus.CmdAsk, Message: "a question"}
	f.add(ask, "bob")
	s = snapshot{person: "bob", agents: map[string]*agentState{},
		asks: []bus.Ask{{ID: rid(5), From: "coder", Title: "a question", State: "open", CreatedAt: foldBase + 5, Questions: []bus.AskQuestionState{{}}}}}
	if its := items(s, f); !held(its, rid(5)) {
		t.Errorf("an ask does not hold its line: %+v", its)
	}

	// A waiting agent's newest report.
	f = newFoldFeed()
	f.add(repEv(0, "master", "## Done"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	s = snapshot{person: "bob", agents: map[string]*agentState{"master": {state: "waiting", stateAt: entryTime(rid(-1))}}}
	if its := items(s, f); !held(its, rid(0)) {
		t.Errorf("a wait does not hold its agent's newest report: %+v", its)
	}

	// A --needs answer report naming one merge item is that item's; one without
	// --needs naming it only attaches (Unread), held by none.
	s = snapshot{person: "bob", agents: map[string]*agentState{}, mergeQ: mqRecord(shellNow), mergeQRead: true}
	f = newFoldFeed()
	needs := repEv(0, "master", "PR 309 is ready")
	needs.Needs = bus.NeedsAnswer
	f.add(needs, "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	if its := items(s, f); !held(its, rid(0)) {
		t.Errorf("a --needs line folded into the merge item is not held: %+v", its)
	}
	f = newFoldFeed()
	f.add(repEv(0, "master", "PR 309 is ready"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	its := items(s, f)
	if held(its, rid(0)) || held(its, rid(1)) {
		t.Errorf("a report without --needs is held: %+v", its)
	}
	attached := false
	for _, it := range its {
		attached = attached || it.Rank == rankMerge && it.Unread
	}
	if !attached {
		t.Errorf("the report no longer attaches to the merge item it names: %+v", its)
	}
}

// N skips each held kind, one case each, and marks a report without
// --needs that only attaches to a merge item (§6 step 1's tests): the guard
// reads Held, so a guard built from IDs alone fails the ask, wait and
// folded --needs cases.
func TestMarkAllSkipsEveryHeldKind(t *testing.T) {
	set := func(s snapshot, f *activityFeed) []string {
		f.classifyWaits(waitingNames(s.agents))
		marks := f.unread.marks(f.feed, nil)
		for i := range f.feed {
			f.feed[i].mark = marks[f.feed[i].ev.ID]
		}
		m := Model{feed: f}
		m.forYou = forYou(s, f.feed)
		return m.markAllSet("")
	}
	none := snapshot{person: "bob", agents: map[string]*agentState{}}
	merge := snapshot{person: "bob", agents: map[string]*agentState{}, mergeQ: mqRecord(shellNow), mergeQRead: true}
	waiting := snapshot{person: "bob", agents: map[string]*agentState{"master": {state: "waiting", stateAt: entryTime(rid(-1))}}}
	asked := snapshot{person: "bob", agents: map[string]*agentState{},
		asks: []bus.Ask{{ID: rid(5), From: "coder", Title: "a question", State: "open", CreatedAt: foldBase + 5, Questions: []bus.AskQuestionState{{}}}}}
	report := func(needs, text string) []bus.Event {
		r := repEv(0, "master", text)
		r.Needs = needs
		return []bus.Event{r, noticeEv(1, 0, "master", "bob")}
	}
	for _, c := range []struct {
		name  string
		s     snapshot
		evs   []bus.Event
		line  string
		marks bool
	}{
		{"a --needs read report item", none, report(bus.NeedsRead, "## Done"), rid(0), false},
		{"an ask's own line", asked, []bus.Event{{Kind: "cmd", ID: rid(5), From: "coder", Target: "bob", Type: bus.CmdAsk, Message: "a question"}}, rid(5), false},
		{"a waiting agent's newest report", waiting, report("", "## Done"), rid(0), false},
		{"a --needs answer report naming one merge item", merge, report(bus.NeedsAnswer, "PR 309 is ready"), rid(0), false},
		{"a report without --needs naming a merge item", merge, report("", "PR 309 is ready"), rid(0), true},
		{"a plain report", none, report("", "## Done"), rid(0), true},
	} {
		f := newFoldFeed()
		for _, e := range c.evs {
			f.add(e, "bob")
		}
		got := set(c.s, f)
		if slices.Contains(got, c.line) != c.marks {
			t.Errorf("%s: N's set %v, marks %s: %v, want %v", c.name, got, c.line, !c.marks, c.marks)
		}
	}
}
