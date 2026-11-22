package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// triage-dedupe step 4 (spec §4): T opens a summary of the actions, one line
// per item; Enter opens an item's page, the walk at that item; Esc on it
// returns to the summary, Esc on the summary ends triage; the last terminal
// act, or an item gone under the walk, returns to the summary, which holds
// the end page's counts and plan-next line.

func walkableItems(m Model) []attentionItem {
	var out []attentionItem
	for _, it := range m.forYou {
		if walkable(it) {
			out = append(out, it)
		}
	}
	return out
}

func TestTriageSummaryOpens(t *testing.T) {
	m := rowsFixture(t, false, false)
	want := walkableItems(m)
	m = keys(t, m, kT)
	p := m.rd.top()
	if p == nil || p.kind != ovTriageSummary || m.ui.walk == nil || !m.ui.walk.end {
		t.Fatalf("T: %+v", p)
	}
	got := m.grid().plain()
	if !strings.Contains(got, fmt.Sprintf("Triage: %d actions", len(want))) || !strings.Contains(got, "answered 0 "+GlyphNone+" sent 0 "+GlyphNone+" read 0") {
		t.Fatalf("the summary:\n%s", got)
	}
	if strings.Contains(got, "triage 1/") {
		t.Fatalf("a walk place on the summary:\n%s", got)
	}
	var keysShown []string
	for _, r := range p.titems {
		if r.plan == "" {
			keysShown = append(keysShown, r.key)
		}
	}
	if last := p.titems[len(p.titems)-1]; last.plan != "p1" {
		t.Fatalf("the plan line last: %+v", last)
	}
	if !slices.Equal(keysShown, itemKeys(want)) {
		t.Fatalf("rows %v, want %v", keysShown, itemKeys(want))
	}
	// Enter: the first item's page, the walk at it; Esc: the summary again.
	m = keys(t, m, kEnter)
	if it, ok := m.walkItem(); !ok || it.Key != want[0].Key {
		t.Fatalf("enter: %+v", it)
	}
	if got := m.walkTitle(); got != fmt.Sprintf("triage 1/%d", len(want)) {
		t.Fatalf("place %q", got)
	}
	m = keys(t, m, kEsc)
	if p := m.rd.top(); p == nil || p.kind != ovTriageSummary || m.ui.walk == nil || !m.walkPage() || !m.ui.walk.end {
		t.Fatalf("esc from the item: %+v", p)
	}
	// p on the summary: back to the item just left; Enter on it again: the
	// same place, not a second visit.
	m = keys(t, m, 'p')
	if it, ok := m.walkItem(); !ok || it.Key != want[0].Key {
		t.Fatalf("p on the summary: %+v", it)
	}
	m = keys(t, m, kEsc, kEnter)
	if got := m.walkTitle(); got != fmt.Sprintf("triage 1/%d", len(want)) {
		t.Fatalf("place after a second Enter %q", got)
	}
	m = keys(t, m, kEsc)
	// ↓ Enter: the second.
	m = keys(t, m, kDown, kEnter)
	if it, ok := m.walkItem(); !ok || it.Key != want[1].Key {
		t.Fatalf("down enter: %+v", it)
	}
	m = keys(t, m, kEsc, kEsc)
	if m.rd.open() || m.ui.walk != nil {
		t.Fatal("esc on the summary did not end triage")
	}
}

// n through every item ends on the summary, with the counts; a terminal act
// on the last unvisited item returns to it too.
func TestTriageSummaryAfterTheLast(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = keys(t, m, kT, kEnter)
	for i := 0; i < 20 && m.rd.top().kind != ovTriageSummary; i++ {
		m = keys(t, m, 'n')
	}
	if m.rd.top().kind != ovTriageSummary || m.ui.walk == nil {
		t.Fatalf("after the last item: %v", m.rd.top().kind)
	}
	if got := m.grid().plain(); !strings.Contains(got, "read 1") {
		t.Fatalf("counts:\n%s", got)
	}
	// Back to a wait (visited), and a send to it: every item visited, so the summary.
	var wait attentionItem
	for i := 0; i < 20; i++ {
		m = keys(t, m, 'p')
		if it, ok := m.walkItem(); ok && it.Rank == rankWait {
			wait = it
			break
		}
	}
	if wait.Key == "" {
		t.Fatal("no wait reached with p")
	}
	m = step(t, m, sent(wait.Agent))
	if m.rd.top().kind != ovTriageSummary || m.ui.walk.outcome[wait.Key] != "sent" {
		t.Fatalf("the last act: %v outcome %q", m.rd.top().kind, m.ui.walk.outcome[wait.Key])
	}
	if got := m.grid().plain(); !strings.Contains(got, "sent 1") {
		t.Fatalf("counts after the send:\n%s", got)
	}
}

// An item gone under the walk (its ask closed elsewhere): back to the summary.
func TestTriageSummaryWhenTheItemGoes(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = keys(t, m, kT, kEnter) // the ask
	if it, ok := m.walkItem(); !ok || it.Rank != rankAsk {
		t.Fatalf("fixture: %+v", it)
	}
	s := m.snap
	s.asks = nil
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	if m.ui.walk == nil || m.rd.top().kind != ovTriageSummary {
		t.Fatalf("the ask gone: %v", m.rd.top().kind)
	}
}

// Nothing waits: the summary says so; its plan-next line opens the task, Esc
// returns to the summary, Esc again ends triage.
func TestTriageSummaryEmpty(t *testing.T) {
	m := rowsFixture(t, false, true)
	m = keys(t, m, kT)
	if m.rd.top().kind != ovTriageSummary || !strings.Contains(m.grid().plain(), "Nothing waits for you") {
		t.Fatalf("empty:\n%s", m.grid().plain())
	}
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p.kind != ovPlanTask || p.key != "p1" {
		t.Fatalf("enter on the plan line: %v %q", p.kind, p.key)
	}
	m = keys(t, m, kEsc)
	if m.rd.top().kind != ovTriageSummary || m.ui.walk == nil {
		t.Fatal("esc from the task did not return to the summary")
	}
	m = keys(t, m, kEsc)
	if m.rd.open() || m.ui.walk != nil {
		t.Fatal("esc on the summary did not end triage")
	}
}
