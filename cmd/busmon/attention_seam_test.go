package main

import (
	"slices"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/attention"
	"github.com/netbja/agent-bus-monitor/bus"
)

// status-summary §3.2, step 1: on one fixture, busmon's For you items whose
// rank is attention's are exactly attention.Items, in order; reports and
// unread lines are busmon's alone.
func TestForYouAttentionRanksAreAttentions(t *testing.T) {
	s, feed := forYouFixture()
	busmon := forYou(s, feed)
	var mine, local []string
	for _, it := range busmon {
		if it.Rank == rankReport || it.Rank == rankUnread {
			local = append(local, it.Key)
			continue
		}
		mine = append(mine, it.Key)
	}
	var theirs []string
	for _, it := range attention.Items(attentionInputs(s), time.Now()) {
		theirs = append(theirs, it.Key)
	}
	if !slices.Equal(mine, theirs) {
		t.Fatalf("busmon's attention ranks %v, attention's %v", mine, theirs)
	}
	if len(local) == 0 || len(theirs) == 0 {
		t.Fatalf("the fixture holds no local rows (%v) or no attention rows (%v)", local, theirs)
	}
}

// A report that asks for an answer, opened but not answered, is still
// busmon's report row; attention never holds it.
func TestReadReportStaysBusmons(t *testing.T) {
	s := targetSnap()
	s.mergeQ.Items = nil
	s.mergeQRead = false
	_, f := itemsOf(s, needsRep(0, "master", bus.NeedsAnswer, "Which notes first?"), needsNotice(1, 0, bus.NeedsAnswer))
	f.unread.read[rid(1)], f.unread.read[rid(0)] = true, true
	marks := f.unread.marks(f.feed, nil)
	for i := range f.feed {
		f.feed[i].mark = marks[f.feed[i].ev.ID]
	}
	items := forYou(s, f.feed)
	if len(items) != 1 || items[0].Rank != rankReport {
		t.Fatalf("busmon's items %+v, want the read report", items)
	}
	for _, it := range attention.Items(attentionInputs(s), time.Now()) {
		if it.Rank == attention.RankReport {
			t.Fatalf("attention holds a report: %+v", it)
		}
	}
}
