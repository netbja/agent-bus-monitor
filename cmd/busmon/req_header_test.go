package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-req-header (Bernard: "Orange = ce qui t'est dû"): the header's
// orange item counts only what is owed to the person, the Requests view's
// own section, so it agrees with the tab's title; what agents owe each other
// is a muted "N between agents", no age, absent at 0.
func TestHeaderRequestsMatchTheTab(t *testing.T) {
	pinLocal(t)
	header := func(m Model) Line {
		return headerRows(m.headerData(), 230, TierWide, 2)[1] // wide enough for the discreet item, the first to go for room
	}
	find := func(l Line, sub string) (Seg, bool) {
		for _, s := range l {
			if strings.Contains(s.Text, sub) {
				return s, true
			}
		}
		return Seg{}, false
	}
	m := fixtureShell(t, 190, 50)
	cmds := reqFixture(shellNow).cmds
	cmds = append(cmds, bus.Event{ID: fmt.Sprintf("%d-0", shellNow.Add(-3*time.Hour).UnixMilli()), Kind: "cmd", Type: bus.CmdDirective,
		From: "master", Target: "bernard", Message: "which tag?"})
	m.snap.cmdHistory = cmds
	m.refresh()
	you, _, agents := requestGroups(m.requestInput()).titleCounts()
	if you == 0 || agents == 0 {
		t.Fatalf("fixture: %d for you, %d between agents", you, agents)
	}
	l := header(m)
	owed, ok := find(l, "waiting")
	if !ok || !strings.HasPrefix(owed.Text, fmt.Sprintf("%d request", you)) || owed.Role != RoleAttention || !strings.Contains(owed.Text, "oldest 3h") {
		t.Errorf("owed item %+v, want %d for you in orange with its oldest (header %q)", owed, you, l.text())
	}
	between, ok := find(l, "between agents")
	if !ok || between.Text != fmt.Sprintf("%d between agents", agents) || between.Role != RoleMuted {
		t.Errorf("between item %+v, want %q muted (header %q)", between, fmt.Sprintf("%d between agents", agents), l.text())
	}
	// Nothing owed to the person: no orange item, the agents' total stays muted.
	m.snap.cmdHistory = reqFixture(shellNow).cmds
	m.snap.agents["coder"].gates = nil // the fixture's gate the person opened
	m.snap.asks = nil
	m.refresh()
	you, _, agents = requestGroups(m.requestInput()).titleCounts()
	l = header(m)
	if _, ok := find(l, "waiting"); ok || you != 0 {
		t.Errorf("%d owed: header %q still has a waiting item", you, l.text())
	}
	if _, ok := find(l, fmt.Sprintf("%d between agents", agents)); !ok {
		t.Errorf("header %q lacks %d between agents", l.text(), agents)
	}
	// Nothing at all: neither item.
	m.snap.cmdHistory = nil
	m.refresh()
	if l = header(m); strings.Contains(l.text(), "waiting") || strings.Contains(l.text(), "between agents") {
		t.Errorf("empty: header %q", l.text())
	}
}

// owedText (foureyes' survivors on busmon-req-header): with two dated owed
// threads the age is the OLDEST, whichever group comes first; one thread
// reads in the singular.
func TestOwedTextOldestAndSingular(t *testing.T) {
	now := shellNow
	thread := func(from string, ago time.Duration) reqGroup {
		return reqGroup{Section: secOwed, From: from, Target: "bernard", Kind: kindDirective,
			Threads: []reqThread{{From: from, Target: "bernard", Kind: kindDirective, At: now.Add(-ago), Section: secOwed}}}
	}
	two := reqView{groups: map[reqSection][]reqGroup{secOwed: {thread("master", 10*time.Minute), thread("foureyes", 3*time.Hour)}}}
	if got, want := owedText(two, now), "2 requests waiting "+GlyphNone+" oldest 3h"; got != want {
		t.Errorf("two owed threads: %q, want %q", got, want)
	}
	one := reqView{groups: map[reqSection][]reqGroup{secOwed: {thread("master", 10*time.Minute)}}}
	if got, want := owedText(one, now), "1 request waiting "+GlyphNone+" oldest 10m"; got != want {
		t.Errorf("one owed thread: %q, want %q", got, want)
	}
	if got := owedText(reqView{questions: 1}, now); got != "1 request waiting" { // a question has no age
		t.Errorf("one open question: %q", got)
	}
	if got := owedText(reqView{}, now); got != "" {
		t.Errorf("nothing owed: %q", got)
	}
}
