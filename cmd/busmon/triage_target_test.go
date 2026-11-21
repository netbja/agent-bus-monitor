package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-dedupe step 2 (spec 2026-11-21-triage-dedupe-design §2, §3; the
// person's §7 answers): For you holds one item per target. A report is an
// item only with --needs; a message naming one live target attaches to it;
// the merge item is keyed by its PR number.

// targetSnap: a merge queue holding PR 694 (feat/dedupe) and PR 695, an open
// ask from master, and the person bob.
func targetSnap() snapshot {
	rec := mqRecord(shellNow)
	rec.Items = []bus.MergeItem{
		{Number: 694, Title: "triage dedupe", Branch: "feat/dedupe", Head: mqH, ApprovedAt: shellNow.Add(-2 * time.Hour).UnixMilli()},
		{Number: 695, Title: "other", Branch: "feat/other", Head: strings.Repeat("c", 40), ApprovedAt: shellNow.Add(-time.Hour).UnixMilli()},
	}
	return snapshot{person: "bob", agents: map[string]*agentState{}, mergeQ: rec, mergeQRead: true}
}

// itemsOf runs the feed's marks then forYou, as refresh does.
func itemsOf(s snapshot, evs ...bus.Event) ([]attentionItem, *activityFeed) {
	f := newFoldFeed()
	for _, e := range evs {
		f.add(e, s.person)
	}
	f.classifyWaits(waitingNames(s.agents))
	marks := f.unread.marks(f.feed, nil)
	for i := range f.feed {
		f.feed[i].mark = marks[f.feed[i].ev.ID]
	}
	return forYou(s, f.feed), f
}

func itemKeys(items []attentionItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out
}

func needsRep(ms int64, agent, needs, msg string) bus.Event {
	e := repEv(ms, agent, msg)
	e.Needs = needs
	return e
}

func needsNotice(ms, reportMs int64, needs string) bus.Event {
	e := noticeEv(ms, reportMs, "master", "bob")
	e.Needs = needs
	return e
}

// Three plain reports naming #694, and its merge item: one row. The reports
// stay in Activity, unread (the person's §7 answer 2: Activity only).
func TestTriageOneItemPerPR(t *testing.T) {
	s := targetSnap()
	items, f := itemsOf(s,
		repEv(0, "master", "merge #694 when you can"), noticeEv(1, 0, "master", "bob"),
		repEv(2, "master", "PR 694 still waits"), noticeEv(3, 2, "master", "bob"),
		repEv(4, "master", "PR #694, then #695"), noticeEv(5, 4, "master", "bob"),
	)
	if got := itemKeys(items); !slices.Equal(got, []string{"merge:pr:694", "merge:pr:695"}) {
		t.Fatalf("items %v", got)
	}
	if n := activityUnread(f.feed); n != 3 {
		t.Fatalf("Activity's unread marks: %d, want the 3 reports", n)
	}
	if unreadItems(items) != 0 {
		t.Fatalf("an unread item: %+v", items)
	}
}

// A --needs answer report naming #694 folds into its item: no new row, the
// item says it needs an answer.
func TestTriageNeedsFoldsIntoItsTarget(t *testing.T) {
	s := targetSnap()
	items, _ := itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "Merge #694 first?"), needsNotice(1, 0, bus.NeedsAnswer))
	if got := itemKeys(items); !slices.Equal(got, []string{"merge:pr:694", "merge:pr:695"}) {
		t.Fatalf("items %v", got)
	}
	if it := items[0]; it.Needs != bus.NeedsAnswer || !it.Unread || !strings.Contains(plainOf(forYouRow(it, 120, shellNow)), "needs your answer") {
		t.Fatalf("the merge item %+v", it)
	}
	// By branch and by PR word too; an ask by its id.
	items, _ = itemsOf(s, needsRep(0, "master", bus.NeedsRead, "feat/other is ready to read"), needsNotice(1, 0, bus.NeedsRead))
	if items[1].Key != "merge:pr:695" || items[1].Needs != bus.NeedsRead || len(items) != 2 {
		t.Fatalf("by branch: %+v", items)
	}
	s.asks = []bus.Ask{{ID: rid(-5), From: "master", Title: "Language", CreatedAt: entryTime(rid(-5)).UnixMilli(), State: "open", Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Header: "L", Question: "which?", Options: []bus.AskOption{{Label: "fr"}}}}}}}
	items, _ = itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "about ask "+rid(-5)+": please answer"))
	if len(items) != 3 || items[0].Key != "ask:"+rid(-5) || items[0].Needs != bus.NeedsAnswer {
		t.Fatalf("by ask id: %+v", items)
	}
}

// A --needs report naming no live target is its own item; "#1" with no PR 1
// attaches nothing; one naming two PRs is its own item.
func TestTriageNeedsOwnItem(t *testing.T) {
	s := targetSnap()
	for _, msg := range []string{"Release notes are ready", "#1 priority: the notes", "Merge order for #694 and #695?"} {
		items, _ := itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, msg), needsNotice(1, 0, bus.NeedsAnswer))
		var own []attentionItem
		for _, it := range items {
			if it.Rank == rankReport {
				own = append(own, it)
			}
		}
		if len(own) != 1 || own[0].Key != "report:"+rid(0) || own[0].Needs != bus.NeedsAnswer || own[0].EventID != rid(1) {
			t.Fatalf("%q: %+v", msg, items)
		}
		for _, it := range items {
			if it.Rank == rankMerge && it.Needs != "" {
				t.Fatalf("%q folded into a merge item: %+v", msg, items)
			}
		}
	}
}

// answer stays until the person writes to its sender, read until it is
// read; a newer --needs report from the same sender naming the same
// targets replaces it.
func TestTriageNeedsLifetime(t *testing.T) {
	s := targetSnap()
	s.mergeQ.Items = nil
	s.mergeQRead = false
	ask := needsRep(0, "master", bus.NeedsAnswer, "Which notes first?")
	items, f := itemsOf(s, ask, needsNotice(1, 0, bus.NeedsAnswer))
	if len(items) != 1 || items[0].Rank != rankReport {
		t.Fatalf("answer: %+v", items)
	}
	f.unread.read[rid(1)], f.unread.read[rid(0)] = true, true // opened: still owed an answer
	marks := f.unread.marks(f.feed, nil)
	for i := range f.feed {
		f.feed[i].mark = marks[f.feed[i].ev.ID]
	}
	if items = forYou(s, f.feed); len(items) != 1 {
		t.Fatalf("an answer report gone once read: %+v", items)
	}
	reply := bus.Event{Kind: "cmd", ID: rid(2), From: "bob", Target: "master", Type: bus.CmdDirective, Message: "the release ones", Human: true}
	if items, _ = itemsOf(s, ask, needsNotice(1, 0, bus.NeedsAnswer), reply); len(items) != 0 {
		t.Fatalf("answered, still listed: %+v", items)
	}
	read := needsRep(0, "master", bus.NeedsRead, "Release notes are ready")
	items, f = itemsOf(s, read, needsNotice(1, 0, bus.NeedsRead))
	if len(items) != 1 {
		t.Fatalf("read: %+v", items)
	}
	f.unread.read[rid(1)], f.unread.read[rid(0)] = true, true
	marks = f.unread.marks(f.feed, nil)
	for i := range f.feed {
		f.feed[i].mark = marks[f.feed[i].ev.ID]
	}
	if items = forYou(s, f.feed); len(items) != 0 {
		t.Fatalf("read, still listed: %+v", items)
	}
	items, _ = itemsOf(s, ask, needsNotice(1, 0, bus.NeedsAnswer), needsRep(2, "master", bus.NeedsAnswer, "Which notes first, v2?"), needsNotice(3, 2, bus.NeedsAnswer))
	if len(items) != 1 || items[0].Key != "report:"+rid(2) {
		t.Fatalf("a newer one replaces: %+v", items)
	}
}

// The merge item is keyed by its PR number: a rebased PR keeps its key;
// with no number, its branch.
func TestTriageMergeKey(t *testing.T) {
	s := targetSnap()
	a := mergeItems(s)
	s.mergeQ.Items[0].Head = strings.Repeat("d", 40)
	b := mergeItems(s)
	if a[0].Key != "merge:pr:694" || b[0].Key != a[0].Key || b[0].Head != strings.Repeat("d", 40) {
		t.Fatalf("rebased: %+v then %+v", a[0], b[0])
	}
	s.mergeQ.Items = []bus.MergeItem{{Branch: "feat/local", Head: mqH, ApprovedAt: shellNow.UnixMilli()}}
	if c := mergeItems(s); c[0].Key != "merge:branch:feat/local" {
		t.Fatalf("no number: %+v", c)
	}
}

// A waiting agent's newest report folds into its wait row (classifyWaits'):
// one row, carrying the mark.
func TestTriageWaitingAgentsReportFolds(t *testing.T) {
	s := snapshot{person: "bob", agents: map[string]*agentState{"coder": {state: "waiting", stateAt: entryTime(rid(-1))}}}
	items, _ := itemsOf(s, repEv(0, "coder", "blocked on the base"))
	if len(items) != 1 || items[0].Rank != rankWait || !items[0].Unread {
		t.Fatalf("%+v", items)
	}
}

// A directive to the person naming #694 attaches to its item; one naming
// nothing stays its own unread row, as today.
func TestTriageOtherLinesAttach(t *testing.T) {
	s := targetSnap()
	dir := func(ms int64, msg string) bus.Event {
		return bus.Event{Kind: "cmd", ID: rid(ms), From: "master", Target: "bob", Type: bus.CmdDirective, Message: msg}
	}
	items, _ := itemsOf(s, dir(0, "PR 694 is approved"))
	if len(items) != 2 || !items[0].Unread {
		t.Fatalf("attached: %+v", items)
	}
	items, _ = itemsOf(s, dir(0, "good morning"))
	if len(items) != 3 || items[2].Rank != rankUnread || items[2].Key != "unread:"+rid(0) {
		t.Fatalf("its own row: %+v", items)
	}
}

// The person's §7 answer 3: two numbers. The header's unread badge counts
// Activity's unread marks (plain reports included), For you's items stay
// the targets'.
func TestTriageHeaderUnreadIsActivity(t *testing.T) {
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	var evs []bus.Event
	for i := int64(0); i < 3; i++ {
		r, n := repEv(2*i, "master", "status"), noticeEv(2*i+1, 2*i, "master", "charlie")
		evs = append(evs, r, n)
	}
	m = step(t, m, tailMsg{events: evs})
	if h := m.headerData(); h.unread != 3 || h.strip.unread != 3 || len(m.forYou) != 0 {
		t.Fatalf("unread badge %d, For you %+v", h.unread, m.forYou)
	}
}
