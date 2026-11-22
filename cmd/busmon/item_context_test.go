package main

import (
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-dedupe step 3 (spec §5): an item's page shows the messages attached
// to it (§2), newest first, at most 3, a --needs report folded into it first.

func ctxIDs(c itemContext) []string {
	var out []string
	for _, l := range c.Lines {
		out = append(out, l.ID)
	}
	return out
}

func TestContextOfAMergeItem(t *testing.T) {
	s := targetSnap()
	_, f := itemsOf(s,
		repEv(0, "master", "merge #694 when you can"),
		repEv(2, "master", "PR 694 still waits"),
		repEv(4, "master", "PR #694, then #695"),
		repEv(6, "master", "#1 priority: nothing named"),
	)
	items := forYou(s, f.feed)
	c := contextOf(items, "merge:pr:694", f.feed)
	if got := ctxIDs(c); !slices.Equal(got, []string{rid(4), rid(2), rid(0)}) || c.Older != 0 {
		t.Fatalf("694: %v older %d", got, c.Older)
	}
	if l := c.Lines[0]; l.Who != "master" || l.Text != "PR #694, then #695" || l.Folded || !l.At.Equal(entryTime(rid(4))) {
		t.Fatalf("a line: %+v", l)
	}
	if got := ctxIDs(contextOf(items, "merge:pr:695", f.feed)); !slices.Equal(got, []string{rid(4)}) {
		t.Fatalf("695: %v", got)
	}
	if c := contextOf(items, "merge:pr:999", f.feed); len(c.Lines) != 0 {
		t.Fatalf("no such item: %+v", c)
	}
}

// At most 3; the rest are counted; a --needs report folded into the item
// comes first and says so, however old.
func TestContextCapAndFoldedFirst(t *testing.T) {
	s := targetSnap()
	_, f := itemsOf(s,
		needsRep(0, "master", bus.NeedsAnswer, "Merge #694 first?"), needsNotice(1, 0, bus.NeedsAnswer),
		repEv(2, "master", "#694 a"), repEv(4, "master", "#694 b"), repEv(6, "master", "#694 c"), repEv(8, "master", "#694 d"),
	)
	items := forYou(s, f.feed)
	c := contextOf(items, "merge:pr:694", f.feed)
	if got := ctxIDs(c); !slices.Equal(got, []string{rid(0), rid(8), rid(6)}) || c.Older != 2 {
		t.Fatalf("got %v older %d", got, c.Older)
	}
	if l := c.Lines[0]; !l.Folded || l.Needs != bus.NeedsAnswer {
		t.Fatalf("the folded report: %+v", l)
	}
	// Four: three shown, one older.
	_, f = itemsOf(s, repEv(2, "master", "#694 a"), repEv(4, "master", "#694 b"), repEv(6, "master", "#694 c"), repEv(8, "master", "#694 d"))
	if c := contextOf(forYou(s, f.feed), "merge:pr:694", f.feed); len(c.Lines) != 3 || c.Older != 1 {
		t.Fatalf("four: %+v", c)
	}
	if c.Lines[1].Folded {
		t.Fatalf("a plain report folded: %+v", c.Lines[1])
	}
}

// A --needs report naming two PRs is its own item and shows in the context
// of each (the person's answer 5), not folded into either.
func TestContextReportNamingTwo(t *testing.T) {
	s := targetSnap()
	_, f := itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "Merge order for #694 and #695?"), needsNotice(1, 0, bus.NeedsAnswer))
	items := forYou(s, f.feed)
	for _, k := range []string{"merge:pr:694", "merge:pr:695"} {
		c := contextOf(items, k, f.feed)
		if len(c.Lines) != 1 || c.Lines[0].ID != rid(0) || c.Lines[0].Folded {
			t.Fatalf("%s: %+v", k, c)
		}
	}
}

// A waiting agent's newest report is its wait's; an older one is not. An
// ask's context holds a line naming its id, never the ask's own line.
func TestContextOfAWaitAndAnAsk(t *testing.T) {
	s := snapshot{person: "bob", agents: map[string]*agentState{"coder": {state: "waiting", stateAt: entryTime(rid(-1))}}}
	_, f := itemsOf(s, repEv(0, "coder", "older"), repEv(2, "coder", "blocked on the base"))
	items := forYou(s, f.feed)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	if got := ctxIDs(contextOf(items, items[0].Key, f.feed)); !slices.Equal(got, []string{rid(2)}) {
		t.Fatalf("wait: %v", got)
	}

	s = targetSnap()
	s.asks = []bus.Ask{{ID: rid(-5), From: "master", Title: "Language", CreatedAt: entryTime(rid(-5)).UnixMilli(), State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Header: "L", Question: "which?", Options: []bus.AskOption{{Label: "fr"}}}}}}}
	_, f = itemsOf(s, bus.Event{Kind: "cmd", ID: rid(-5), From: "master", Target: "bob", Type: bus.CmdAsk, Message: "Language"},
		repEv(0, "master", "about ask "+rid(-5)+": the options"))
	items = forYou(s, f.feed)
	if got := ctxIDs(contextOf(items, "ask:"+rid(-5), f.feed)); !slices.Equal(got, []string{rid(0)}) {
		t.Fatalf("ask: %v", got)
	}
}

// Master's answer to Q2: a line also attaches when it replies on the item's
// thread (its ref is the ask, the gate's challenge, or the report); a
// report's own notices are not replies.
func TestContextThreadReplies(t *testing.T) {
	reply := func(ms int64, ref, msg string) bus.Event {
		return bus.Event{Kind: "cmd", ID: rid(ms), From: "master", Target: "bob", Type: bus.CmdDirective, Ref: ref, Message: msg}
	}
	// A gate on the person: its challenge's replies.
	s := targetSnap()
	gateRef := rid(-9)
	s.personGates = map[string]string{gateRef: "master|sign off the release"}
	_, f := itemsOf(s, reply(0, gateRef, "the release notes are attached"), reply(2, rid(-50), "elsewhere"))
	items := forYou(s, f.feed)
	if got := ctxIDs(contextOf(items, "gate:bob:"+gateRef, f.feed)); !slices.Equal(got, []string{rid(0)}) {
		t.Fatalf("gate: %v (items %v)", got, itemKeys(items))
	}
	// A --needs report's own item: a reply on it, not its notice.
	s = targetSnap()
	_, f = itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "Release notes are ready"), needsNotice(1, 0, bus.NeedsAnswer),
		reply(2, rid(0), "page 3 changed"))
	items = forYou(s, f.feed)
	if got := ctxIDs(contextOf(items, "report:"+rid(0), f.feed)); !slices.Equal(got, []string{rid(2)}) {
		t.Fatalf("report: %v (items %v)", got, itemKeys(items))
	}
	// Its notice alone (the report out of reach): the item, not a reply to it.
	_, f = itemsOf(s, needsNotice(1, 0, bus.NeedsAnswer))
	items = forYou(s, f.feed)
	if c := contextOf(items, "report:"+rid(0), f.feed); len(c.Lines) != 0 || !slices.Contains(itemKeys(items), "report:"+rid(0)) {
		t.Fatalf("a notice alone: %+v (items %v)", c, itemKeys(items))
	}
	// An ask: a reply on it.
	s.asks = []bus.Ask{{ID: rid(-5), From: "master", Title: "Language", CreatedAt: entryTime(rid(-5)).UnixMilli(), State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Header: "L", Question: "which?", Options: []bus.AskOption{{Label: "fr"}}}}}}}
	_, f = itemsOf(s, reply(0, rid(-5), "the options explained"))
	items = forYou(s, f.feed)
	if got := ctxIDs(contextOf(items, "ask:"+rid(-5), f.feed)); !slices.Equal(got, []string{rid(0)}) {
		t.Fatalf("ask: %v", got)
	}
}

// foureyes: Folded is foldLines' own decision. A --needs report read (or
// answered) no longer folds, so it is no longer first; a waiting agent's
// newest --needs report folds into its wait, not into the PR it names.
func TestContextFoldedAsForYouFolds(t *testing.T) {
	s := targetSnap()
	_, f := itemsOf(s, needsRep(0, "master", bus.NeedsRead, "Read #694's notes"), needsNotice(1, 0, bus.NeedsRead),
		repEv(2, "master", "#694 later"))
	f.unread.read[rid(0)], f.unread.read[rid(1)] = true, true
	marks := f.unread.marks(f.feed, nil)
	for i := range f.feed {
		f.feed[i].mark = marks[f.feed[i].ev.ID]
	}
	items := forYou(s, f.feed)
	c := contextOf(items, "merge:pr:694", f.feed)
	if items[0].Needs != "" || len(c.Lines) != 2 || c.Lines[0].ID != rid(2) || c.Lines[1].Folded {
		t.Fatalf("read: needs %q, %+v", items[0].Needs, c)
	}
	// Answered from its page: the same.
	_, f = itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "Merge #694 first?"), needsNotice(1, 0, bus.NeedsAnswer), repEv(2, "master", "#694 later"))
	for i := range f.feed {
		if f.feed[i].ev.ID == rid(0) {
			f.feed[i].answered = true
		}
	}
	items = forYou(s, f.feed)
	if c := contextOf(items, "merge:pr:694", f.feed); items[0].Needs != "" || c.Lines[0].ID != rid(2) || c.Lines[1].Folded {
		t.Fatalf("answered: needs %q, %+v", items[0].Needs, c)
	}
	// A waiting agent's newest --needs report: folded into its wait only.
	s.agents = map[string]*agentState{"coder": {state: "waiting", stateAt: entryTime(rid(-1))}}
	_, f = itemsOf(s, needsRep(0, "coder", bus.NeedsAnswer, "Rebase #694 or not?"))
	items = forYou(s, f.feed)
	wait := items[slices.IndexFunc(items, func(it attentionItem) bool { return it.Rank == rankWait })]
	if c := contextOf(items, wait.Key, f.feed); len(c.Lines) != 1 || !c.Lines[0].Folded {
		t.Fatalf("the wait: %+v", c)
	}
	if c := contextOf(items, "merge:pr:694", f.feed); len(c.Lines) != 1 || c.Lines[0].Folded {
		t.Fatalf("the merge page: %+v", c)
	}
}

// foureyes (non-blocking on step 3): only the waiting agent's NEWEST report
// folds into its wait; an older --needs report of it naming #694 folds into
// 694, so its Context line there is folded.
func TestContextWaitingAgentsOlderReport(t *testing.T) {
	s := targetSnap()
	s.agents = map[string]*agentState{"coder": {state: "waiting", stateAt: entryTime(rid(-1))}}
	_, f := itemsOf(s, needsRep(0, "coder", bus.NeedsAnswer, "Rebase #694 or not?"), repEv(2, "coder", "blocked on the base"))
	items := forYou(s, f.feed)
	if i := slices.IndexFunc(items, func(it attentionItem) bool { return it.Key == "merge:pr:694" }); i < 0 || items[i].Needs != bus.NeedsAnswer {
		t.Fatalf("694 not folded into: %+v", items)
	}
	c := contextOf(items, "merge:pr:694", f.feed)
	if len(c.Lines) != 1 || c.Lines[0].ID != rid(0) || !c.Lines[0].Folded {
		t.Fatalf("694's context: %+v", c)
	}
}
