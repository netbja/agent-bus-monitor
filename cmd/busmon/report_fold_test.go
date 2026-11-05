package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// report-one-row §2: a report and its notices are one Activity line.

const foldBase = int64(1793900000000)

func rid(ms int64) string { return fmt.Sprintf("%d-0", foldBase+ms) }

func repEv(ms int64, agent, msg string) bus.Event {
	return bus.Event{Kind: "report", ID: rid(ms), Agent: agent, RKind: "note", Message: msg}
}

func noticeEv(ms int64, reportMs int64, from, target string) bus.Event {
	id := rid(reportMs)
	return bus.Event{Kind: "cmd", ID: rid(ms), From: from, Target: target, Type: bus.CmdDirective, Ref: id,
		Message: "Report " + id + " for you (full text: agentbus reports " + id + "): Done"}
}

func statusEv(ms int64) bus.Event {
	return bus.Event{Kind: "status", ID: rid(ms), Agent: "coder", State: "working"}
}

func newFoldFeed() *activityFeed { return &activityFeed{unread: newUnreadState()} }

// events are the feed's event lines (no separators).
func events(f *activityFeed) []feedLine {
	var out []feedLine
	for _, fl := range f.feed {
		if !fl.sep {
			out = append(out, fl)
		}
	}
	return out
}

func TestFoldReportThenNotice(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "## Done\n"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	ls := events(f)
	if len(ls) != 1 || ls[0].ev.Kind != "report" || len(ls[0].notices) != 1 || ls[0].notices[0].Target != "bob" {
		t.Fatalf("want one report line holding its notice: %+v", ls)
	}
	if !f.unread.forPerson[rid(1)] {
		t.Fatal("the folded notice is still for the person")
	}
}

func TestFoldNoticeThenReportTakesOverTheLine(t *testing.T) {
	f := newFoldFeed()
	f.add(noticeEv(0, 2, "master", "bob"), "bob")
	before := events(f)[0]
	f.add(statusEv(1), "bob")
	f.add(repEv(2, "master", "## Done"), "bob")
	ls := events(f)
	if len(ls) != 2 {
		t.Fatalf("want the notice line and the status line: %d", len(ls))
	}
	l := ls[0]
	if l.ev.Kind != "report" || l.id != before.id || l.seq != before.seq || len(l.notices) != 1 || l.notices[0].ID != rid(0) {
		t.Fatalf("the report did not take the notice's line over in place: %+v (was id %s seq %d)", l, before.id, before.seq)
	}
	if !l.at.Equal(entryTime(rid(2))) {
		t.Fatalf("the line shows the report's time: %v", l.at)
	}
}

func TestFoldNoticeWithoutItsReportStaysALine(t *testing.T) {
	f := newFoldFeed()
	f.add(noticeEv(0, -5, "master", "bob"), "bob")
	ls := events(f)
	if len(ls) != 1 || ls[0].ev.Kind != "cmd" || len(ls[0].notices) != 0 {
		t.Fatalf("a notice with no report at hand is its own line: %+v", ls)
	}
}

func TestFoldTwoTargetsOneLine(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "x"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	f.add(noticeEv(2, 0, "master", "coder"), "bob")
	ls := events(f)
	if len(ls) != 1 || len(ls[0].notices) != 2 {
		t.Fatalf("two --to targets, one line: %+v", ls)
	}
	if got := ls[0].text; got == "" || !containsAll(got, "-> bob, coder") {
		t.Fatalf("the plain line names the targets: %q", got)
	}
}

func TestFoldNeedsTheExactRef(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "x"), "bob")
	n := noticeEv(1, 0, "master", "bob")
	n.Ref = "other-0" // a forged prefix: its id is not its ref
	f.add(n, "bob")
	if ls := events(f); len(ls) != 2 {
		t.Fatalf("a notice whose ref is not its report id folds nothing: %d lines", len(ls))
	}
}

func TestFoldPastFeedCap(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "old"), "bob")
	for i := int64(1); i <= feedCap+5; i++ {
		f.add(statusEv(i), "bob")
	}
	if _, ok := f.reportLine[rid(0)]; ok {
		t.Fatal("a report trimmed from the feed leaves reportLine")
	}
	f.add(noticeEv(feedCap+10, 0, "master", "bob"), "bob")
	last := f.feed[len(f.feed)-1]
	if last.ev.Kind != "cmd" || last.ev.ID != rid(feedCap+10) {
		t.Fatalf("its late notice is a line of its own: %+v", last)
	}
	if len(f.reportLine) > 0 || len(f.noticeLine) > 1 {
		t.Fatalf("the maps hold only lines still in the feed: %v %v", f.reportLine, f.noticeLine)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

// §2: the folded line carries its notice's mark; reading the line reads
// every member.
func TestFoldMarkIsTheNotices(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "## Done\n- Vérif: ok"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	marks := f.unread.marks(f.feed, nil)
	if marks[rid(0)] != markUnread {
		t.Fatalf("the report line is unread through its notice: %v", marks)
	}
	for _, id := range lineIDs(events(f)[0]) {
		f.unread.read[id] = true
	}
	if marks := f.unread.marks(f.feed, nil); marks[rid(0)] != markNone {
		t.Fatalf("read once opened: %v", marks)
	}
	if ids := lineIDs(events(f)[0]); len(ids) != 2 || ids[0] != rid(0) || ids[1] != rid(1) {
		t.Fatalf("lineIDs: %v", ids)
	}
}

// §3: a waiting agent's report with a notice to the person is one For you
// row (the notice's, holding all its ids); one whose only notice went to an
// agent keeps its own row.
func TestForYouOneRowPerReport(t *testing.T) {
	waiting := &agentState{state: "waiting", stateAt: entryTime(rid(-1))}
	s := snapshot{person: "bob", agents: map[string]*agentState{"master": waiting}}
	rows := func(f *activityFeed) []attentionItem {
		f.classifyWaits([]string{"master"})
		marks := f.unread.marks(f.feed, nil)
		for i := range f.feed {
			f.feed[i].mark = marks[f.feed[i].ev.ID]
		}
		var out []attentionItem
		for _, it := range forYou(s, f.feed) {
			if it.Rank == rankUnread {
				out = append(out, it)
			}
		}
		return out
	}

	f := newFoldFeed()
	f.add(repEv(0, "master", "## Done\n- Vérif: ok"), "bob")
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	f.add(noticeEv(2, 0, "master", "bob"), "bob") // a re-send
	got := rows(f)
	if len(got) != 1 || got[0].Key != "unread:"+rid(1) || got[0].Text != "Vérif: ok" || len(got[0].IDs) != 2 {
		t.Fatalf("one row, the notice's key, the headline, both ids: %+v", got)
	}

	f = newFoldFeed()
	f.add(repEv(0, "master", "to coder"), "bob")
	f.add(noticeEv(1, 0, "master", "coder"), "bob")
	got = rows(f)
	if len(got) != 1 || got[0].Key != "unread:"+rid(0) {
		t.Fatalf("a report whose notice went to an agent keeps its own row: %+v", got)
	}
}

// §4 in busmon: an Activity report row reads as its headline, with its
// targets in the to column.
func TestFoldRowReadsTheHeadline(t *testing.T) {
	f := newFoldFeed()
	f.add(repEv(0, "master", "## Done - Vérif: ok"), "bob")
	f.feed[len(f.feed)-1].ev.Full = "## Done\n- Vérif: ok"
	f.add(noticeEv(1, 0, "master", "bob"), "bob")
	row := activityRowAt(events(f)[0], TierWide, 120, entryTime(rid(0))).text()
	if !strings.Contains(row, "Vérif: ok") || strings.Contains(row, "##") || !strings.Contains(row, GlyphArrowR+" bob") {
		t.Fatalf("row: %q", row)
	}
	// An old notice alone reads as its subject's headline.
	f = newFoldFeed()
	f.add(noticeEv(0, -3, "master", "bob"), "bob")
	if row := activityRowAt(events(f)[0], TierWide, 120, entryTime(rid(0))).text(); !strings.Contains(row, "Done") || strings.Contains(row, "Report ") {
		t.Fatalf("notice row: %q", row)
	}
}

// §3: the recap reads a notice as its report's headline, old envelopes too.
func TestRecapNoticeReadsTheHeadline(t *testing.T) {
	text, id := reportText(noticeEv(1, 0, "master", "bob").Message)
	if text != "Done" || id != rid(0) {
		t.Fatalf("reportText: %q %q", text, id)
	}
	old := "Report 1-0 for you (full text: agentbus reports 1-0): ## Done"
	if text, _ := reportText(old); text != "Done" {
		t.Fatalf("an old notice's heading reads as its text: %q", text)
	}
}

// §4: i's quote is the headline, not a heading.
func TestReplyQuoteIsTheHeadline(t *testing.T) {
	ev := bus.Event{Kind: "report", Agent: "master", Message: "## Done - Vérif: ok", Full: "## Done\n- Vérif: ok"}
	if got := replyPrefill(ev); got != "re: Vérif: ok\n" {
		t.Fatalf("replyPrefill: %q", got)
	}
}

// §5 B: opening a report row writes the read receipt for its notices to the
// person, once; a report with no such notice writes none.
func TestOpeningAReportRowWritesTheReceipt(t *testing.T) {
	fw := &fakeWriter{}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, false)
	at := shellNow.Add(-time.Minute).UnixMilli()
	rep := bus.Event{Kind: "report", ID: fmt.Sprintf("%d-0", at), Agent: "master", RKind: "note", Message: "## Done"}
	n := bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-1", at), From: "master", Target: "charlie", Type: bus.CmdDirective, Ref: rep.ID,
		Message: "Report " + rep.ID + " for you (full text: agentbus reports " + rep.ID + "): Done"}
	other := bus.Event{Kind: "report", ID: fmt.Sprintf("%d-2", at), Agent: "coder", RKind: "note", Message: "x"}
	m = step(t, m, tailMsg{events: []bus.Event{rep, n, other}})
	seen := func() []string {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		var out []string
		for _, s := range fw.sent {
			if strings.HasPrefix(s, "seen ") {
				out = append(out, s)
			}
		}
		return out
	}
	m.markLineRead(rep.ID)
	m = run(t, m, m.afterUpdate())
	if got := seen(); len(got) != 1 || got[0] != "seen charlie "+n.ID {
		t.Fatalf("receipt: %v", got)
	}
	m.markLineRead(rep.ID)
	m.markLineRead(other.ID)
	m = run(t, m, m.afterUpdate())
	if got := seen(); len(got) != 1 {
		t.Fatalf("a second opening, or a report with no notice, wrote again: %v", got)
	}
	if !slices.Contains(m.cache_().Read, n.ID) {
		t.Fatalf("the folded notice's read mark is kept in the cache: %v", m.cache_().Read)
	}
}

// The real writer writes the receipt.
var _ seenWriter = (*bus.Bus)(nil)
