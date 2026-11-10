package main

import (
	"strings"
	"testing"
	"time"
)

// reminderRow is the screen row inside the open reminder whose text holds s.
func reminderRow(t *testing.T, m Model, s string) int {
	t.Helper()
	r := m.reminderRect()
	lines := strings.Split(m.grid().plain(), "\n")
	for y := r.Y + 1; y < r.Y+r.H-1 && y < len(lines); y++ {
		if strings.Contains(lines[y], s) {
			return y
		}
	}
	t.Fatalf("%q not in the reminder:\n%s", s, m.grid().plain())
	return -1
}

// remindedShell is the shell with the reminder open on two items: master's
// ask and foureyes' wait (the snooze over, as in TestShellReminder).
func remindedShell(t *testing.T) Model {
	t.Helper()
	set := busmonSettings{PopupAfter: 5 * time.Minute, Snooze: 15 * time.Minute}
	m := shellWith(t, 140, 40, shellConfig{settings: set}, false)
	m = keys(t, m, kEsc)
	m = pollAgain(t, m, shellFixture(shellNow), shellNow.Add(16*time.Minute))
	if m.ui.reminder == nil || len(m.ui.reminder.items) != 2 {
		t.Fatalf("setup: reminder %+v", m.ui.reminder)
	}
	return m
}

func reminderItem(t *testing.T, m Model, rank int) attentionItem {
	t.Helper()
	for _, it := range m.ui.reminder.items {
		if it.Rank == rank {
			return it
		}
	}
	t.Fatalf("no item of rank %d in %+v", rank, m.ui.reminder.items)
	return attentionItem{}
}

// The person's ask (popup-click): a click on an item of "Waiting for you"
// opens what it announces, as Enter on it does: an ask its answer page, a
// wait the compose window to its agent. Either of the item's two lines takes
// the click.
func TestReminderClickActsOnTheItem(t *testing.T) {
	pinLocal(t)
	m := remindedShell(t)
	if !strings.Contains(m.grid().plain(), "enter or click act") {
		t.Fatalf("the hint does not offer the click:\n%s", m.grid().plain())
	}
	ask := reminderItem(t, m, rankAsk)
	x := m.reminderRect().X + 4
	m, cmd := clickCmd(t, m, x, reminderRow(t, m, Preview(ask.Text)))
	m = run(t, m, cmd)
	if p := m.rd.top(); m.ui.reminder != nil || p == nil || p.kind != ovAsk || p.key != ask.AskID {
		t.Fatalf("click on the ask: reminder %+v, page %+v", m.ui.reminder, m.rd.top())
	}

	m = remindedShell(t)
	wait := reminderItem(t, m, rankWait)
	y := reminderRow(t, m, forYouKinds[rankWait].word+" "+Preview(wait.Agent))
	if m = clickAt(t, m, x, y); m.ui.reminder != nil || m.ui.compose == nil || m.ui.compose.to.value() != "@"+wait.Agent {
		t.Fatalf("click on %s's wait: reminder %+v, compose %+v", wait.Agent, m.ui.reminder, m.ui.compose)
	}
}

// A click on no item (the frame, the blank line between two items, outside
// the popup) leaves the reminder open, its selection unchanged, and opens
// nothing: the reminder stays modal.
func TestReminderClickOffAnItemDoesNothing(t *testing.T) {
	pinLocal(t)
	m := remindedShell(t)
	r := m.reminderRect()
	for _, at := range [][2]int{
		{r.X + 4, r.Y},           // the title row
		{r.X + 4, r.Y + 3},       // the blank line after the first item
		{r.X + 4, r.Y + r.H - 1}, // the hint row
		{r.X, r.Y + 1},           // the frame's left side
		{0, 0},                   // outside, on the header
		{r.X + 4, r.Y + r.H + 1}, // outside, below
	} {
		m = clickAt(t, m, at[0], at[1])
		if m.ui.reminder == nil || m.ui.reminder.sel != 0 || m.ui.compose != nil || m.rd.open() {
			t.Fatalf("click at %v: reminder %+v, compose %v, reader %v", at, m.ui.reminder, m.ui.compose != nil, m.rd.open())
		}
	}
}

// Every kind of item routes by click as Enter routes it (foureyes P3 b): a
// gate opens its challenge's thread, health the health page, a merge its
// page; none falls through to the compose window.
func TestReminderClickRoutesEachKind(t *testing.T) {
	pinLocal(t)
	at := shellNow.Add(-10 * time.Minute)
	for _, c := range []struct {
		it   attentionItem
		kind overlayKind
		key  string
	}{
		{attentionItem{Rank: rankGate, Key: "gate:coder:1793026685000-0", Agent: "coder", By: "foureyes", Text: "review lot 1", At: at}, ovMessage, "1793026685000-0"},
		{attentionItem{Rank: rankHealth, Key: "health:team-stall:1", Agent: "stall", Text: "the team waits on you", At: at}, ovHealth, ""},
		{attentionItem{Rank: rankMerge, Key: "merge:pr:42", Agent: "master", Text: "PR 42", At: at}, ovMerge, "pr:42"},
	} {
		m := shellWith(t, 140, 40, shellConfig{}, false)
		m.ui.reminder = &reminderDialog{items: []attentionItem{c.it}}
		m.refresh()
		m, cmd := clickCmd(t, m, m.reminderRect().X+4, reminderRow(t, m, Preview(c.it.Text)))
		m = run(t, m, cmd)
		p := m.rd.top()
		if m.ui.reminder != nil || m.ui.compose != nil || p == nil || p.kind != c.kind || c.key != "" && p.key != c.key {
			t.Fatalf("click on a rank %d item: reminder %+v, compose %v, page %+v", c.it.Rank, m.ui.reminder, m.ui.compose != nil, p)
		}
	}
}

// When the screen clips the reminder (more items than rows), a click on its
// bottom row, the frame and its hint, acts on nothing, not on an item cut
// from view (foureyes P3 a).
func TestReminderClickClippedBottomRow(t *testing.T) {
	pinLocal(t)
	for n := 13; n < 16; n++ { // three heights: the bottom row falls on each line of an item
		m := shellWith(t, 140, 40, shellConfig{}, false)
		var items []attentionItem
		for i := range n {
			items = append(items, attentionItem{Rank: rankWait, Key: "wait:a" + string(rune('a'+i)), Agent: "a" + string(rune('a'+i)), Text: "w", At: shellNow})
		}
		m.ui.reminder = &reminderDialog{items: items}
		m.refresh()
		r := m.reminderRect()
		if r.H >= reminderItemLines*n+2 {
			t.Fatalf("setup: the reminder is not clipped (%d rows for %d items)", r.H, n)
		}
		// The items are no decisions of the fixture: the update's prune closes
		// the reminder after the click; what matters is that the click opened
		// nothing (on an item, a wait opens the compose window).
		if m = clickAt(t, m, r.X+4, r.Y+r.H-1); m.ui.compose != nil || m.rd.open() {
			t.Fatalf("%d items: a click on the clipped bottom row acted (compose %v, reader %v)", n, m.ui.compose != nil, m.rd.open())
		}
	}
}
