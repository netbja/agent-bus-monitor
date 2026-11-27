package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-unread step 2 (spec docs/superpowers/specs/2026-11-26-triage-unread-design.md
// §3): the triage summary's last line counts the unread Activity lines no
// triage item holds (§4's set); none, no line; u on the summary ends triage
// and selects the first such line in Activity.

func selectedFeedID(m Model) string {
	for _, fl := range m.feed.feed {
		if !fl.sep && fl.id == m.lists.activity.Selected() {
			return fl.ev.ID
		}
	}
	return ""
}

func TestTriageSummaryUnreadLine(t *testing.T) {
	held := mrReport(70_000, "release", bus.NeedsRead, "read the release notes")
	plain := mrReport(60_000, "release", "", "tagged v1")
	plain2 := mrReport(50_000, "docs", "", "docs out")
	m := mrShell(t, append(append(held, plain...), plain2...)...)
	set := m.markAllSet("")
	if len(set) != 2 || set[0] != plain[0].ID {
		t.Fatalf("§4's set %v, want the two plain reports", set)
	}
	if i, j := slices.IndexFunc(m.feed.feed, func(fl feedLine) bool { return fl.ev.ID == held[0].ID }),
		slices.IndexFunc(m.feed.feed, func(fl feedLine) bool { return fl.ev.ID == plain[0].ID }); i < 0 || i > j {
		t.Fatalf("the held line is not first in Activity (%d, %d): u would not tell them apart", i, j)
	}
	m.focusPanel(PanelAgents) // u moves the focus itself
	m = keys(t, m, kT)
	if p := m.rd.top(); p == nil || p.kind != ovTriageSummary {
		t.Fatalf("T: %+v", p)
	}
	got := m.grid().plain()
	want := fmt.Sprintf("%d unread in Activity, none needs you", len(set))
	if !strings.Contains(got, want) || !strings.Contains(got, GlyphArrowR+"  u") {
		t.Fatalf("the summary has no %q:\n%s", want, got)
	}
	// u: triage ends, Activity, the first unread line no item holds.
	m = keys(t, m, 'u')
	if m.rd.top() != nil || m.ui.walk != nil {
		t.Fatalf("u did not end triage: %+v", m.rd.top())
	}
	if m.ui.focus != PanelActivity || selectedFeedID(m) != set[0] {
		t.Fatalf("u: focus %v, selected %s, want %s", m.ui.focus, selectedFeedID(m), set[0])
	}
}

// With every line outside triage read, the line is absent and u ends nothing.
func TestTriageSummaryNoUnreadLine(t *testing.T) {
	m := mrShell(t, mrReport(40_000, "release", bus.NeedsRead, "read it")...)
	for _, id := range m.markAllSet("") {
		m.markLineRead(id)
	}
	m.refresh()
	m = keys(t, m, kT)
	if got := m.grid().plain(); strings.Contains(got, "unread in Activity") {
		t.Fatalf("a line with nothing to count:\n%s", got)
	}
	// u there: the summary stays, and says so.
	m = keys(t, m, 'u')
	if p := m.rd.top(); p == nil || p.kind != ovTriageSummary || m.ui.walk == nil || !strings.Contains(m.ui.flash, "nothing unread outside triage") {
		t.Fatalf("u with none: %+v, flash %q", m.rd.top(), m.ui.flash)
	}
}
