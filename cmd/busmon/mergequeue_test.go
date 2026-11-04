package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const mqH = "f98091be429b220f63dc8b4b2dde3ef7d6d832a5"

func mqRecord(at time.Time) bus.MergeQueueRecord {
	return bus.MergeQueueRecord{AttemptedAt: at.UnixMilli(), ItemsAt: at.UnixMilli(), Source: "gh",
		Items: []bus.MergeItem{{Number: 309, Title: "request priority", URL: "https://github.com/o/r/pull/309", Branch: "feat/request-priority",
			Head: mqH, Subjects: []string{"request-priority"}, ApprovedAt: at.Add(-time.Hour).UnixMilli(),
			Approvals: []bus.MergeApproval{{Reviewer: "foureyes", At: at.Add(-time.Hour).UnixMilli(), Message: "full suite and Redis suite pass"},
				{Reviewer: "reviewer-codex", At: at.Add(-2 * time.Hour).UnixMilli(), Message: "no findings"}}}}}
}

func TestMergeItemsForYou(t *testing.T) {
	s := snapshot{mergeQ: mqRecord(shellNow), mergeQRead: true}
	items := mergeItems(s)
	if len(items) != 1 || items[0].Rank != rankMerge || items[0].Key != "merge:"+mqH || items[0].Agent != "you" ||
		items[0].Text != "merge PR 309: request priority" || !items[0].At.Equal(time.UnixMilli(s.mergeQ.Items[0].ApprovedAt)) {
		t.Fatalf("%+v", items)
	}
	if d := decisions(items); len(d) != 1 {
		t.Fatalf("a waiting merge is a decision: %+v", d)
	}
	// The git fallback: no number, the branch.
	s.mergeQ.Items[0].Number, s.mergeQ.Items[0].Title = 0, ""
	if it := mergeItems(s)[0]; it.Text != "merge feat/request-priority" {
		t.Fatalf("%q", it.Text)
	}
}

// A failed pass keeps the items as of their time; nothing read at all is one
// row that says so, never a decision; no reader, no row.
func TestMergeItemsUnknown(t *testing.T) {
	rec := mqRecord(shellNow.Add(-time.Hour))
	rec.Reason, rec.AttemptedAt = "gh timed out", shellNow.UnixMilli()
	items := mergeItems(snapshot{mergeQ: rec, mergeQRead: true})
	if len(items) != 1 || !strings.Contains(items[0].Text, "(as of "+shellNow.Add(-time.Hour).Local().Format("15:04")+": gh timed out)") {
		t.Fatalf("%+v", items)
	}
	none := mergeItems(snapshot{mergeQ: bus.MergeQueueRecord{AttemptedAt: shellNow.UnixMilli(), Reason: "no origin remote"}, mergeQRead: true})
	if len(none) != 1 || none[0].Key != mergeUnknownKey || !strings.Contains(none[0].Text, "no origin remote") || len(decisions(none)) != 0 {
		t.Fatalf("unknown: %+v", none)
	}
	never := mergeItems(snapshot{mergeQRead: true})
	if len(never) != 1 || never[0].Key != mergeUnknownKey || !strings.Contains(never[0].Text, "not computed") {
		t.Fatalf("no record: %+v", never)
	}
	if got := mergeItems(snapshot{mergeQErr: "read failed", mergeQRead: true}); len(got) != 1 || !strings.Contains(got[0].Text, "read failed") {
		t.Fatalf("a failed read: %+v", got)
	}
	if got := mergeItems(snapshot{}); len(got) != 0 {
		t.Fatalf("no reader: %+v", got)
	}
	// An empty success says nothing: nothing waits.
	ok := bus.MergeQueueRecord{AttemptedAt: 1, ItemsAt: 1, Source: "gh"}
	if got := mergeItems(snapshot{mergeQ: ok, mergeQRead: true}); len(got) != 0 {
		t.Fatalf("an empty queue: %+v", got)
	}
}

// Enter opens the item's page: the PR, the branch and head, who approved and
// what they wrote; y copies the URL, Y the reviewers' messages.
func TestMergePage(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	snap := m.snap
	snap.mergeQ, snap.mergeQRead = mqRecord(shellNow), true
	m.snap = snap
	m.refresh()
	m.push(page{kind: ovMerge, key: mqH})
	p := m.rd.top()
	var text strings.Builder
	for _, l := range p.body {
		text.WriteString(l.text() + "\n")
	}
	for _, want := range []string{"PR:", "309", "request priority", "Branch:", "feat/request-priority", mqH[:12], "Approved by:", "foureyes",
		"reviewer-codex", "full suite and Redis suite pass", "Subject:", "request-priority", "Source:", "gh"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("page lacks %q:\n%s", want, text.String())
		}
	}
	if p.plain != "https://github.com/o/r/pull/309" || !strings.Contains(p.threadPlain, "full suite and Redis suite pass") {
		t.Fatalf("y %q, Y %q", p.plain, p.threadPlain)
	}
	// Under the local forge, y copies the merge command.
	snap.mergeQ.Items[0].Number, snap.mergeQ.Items[0].URL, snap.mergeQ.Source, snap.mergeQ.Forge, snap.mergeQ.Base = 0, "", "git", "local", "main"
	m.snap = snap
	m.closeReader()
	m.push(page{kind: ovMerge, key: mqH})
	if p := m.rd.top(); p.plain != "git switch main && git merge --no-ff "+mqH {
		t.Fatalf("local y %q", p.plain)
	}
}

// The screen with a merge waiting: its For you row at the wide and medium tiers.
func TestMergeQueueGoldens(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{140, 40}, {100, 30}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := fixtureShell(t, sz[0], sz[1])
			snap := m.snap
			snap.mergeQ, snap.mergeQRead = mqRecord(shellNow), true
			m.snap = snap
			m.refresh()
			m.paint = newPainter(th)
			plain := m.grid().plain()
			if !strings.Contains(plain, "merge PR 309") {
				t.Fatalf("no merge row:\n%s", plain)
			}
			checkGolden(t, fmt.Sprintf("mergequeue-%dx%d.txt", sz[0], sz[1]), plain+"\n")
		})
	}
}
