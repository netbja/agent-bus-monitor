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
