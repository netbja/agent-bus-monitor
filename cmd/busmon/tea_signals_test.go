package main

import (
	"strings"
	"testing"
	"time"
)

// pollAgain is a poll of snap a second later, with judged agents.
func pollAgain(t testing.TB, m Model, snap snapshot, at time.Time) Model {
	t.Helper()
	for _, a := range snap.agents {
		a.judgedAt = at
	}
	return step(t, m, pollMsg{snap: snap, at: at})
}

// The bell rings once per new fact: a new waiting declaration, a new ask id,
// a new observed-prompt episode; the same facts again ring nothing; bell =
// false rings never.
func TestShellBells(t *testing.T) {
	pinLocal(t)
	on := busmonSettings{Bell: true}
	m := shellWith(t, 140, 40, shellConfig{settings: on}, false)
	if m.sig.bells != 1 {
		t.Fatalf("first poll (an ask, a wait, a prompt): %d bells, want 1", m.sig.bells)
	}
	at := shellNow.Add(time.Second)
	if m = pollAgain(t, m, shellFixture(shellNow), at); m.sig.bells != 1 {
		t.Fatalf("the same facts rang again: %d", m.sig.bells)
	}
	for _, c := range []struct {
		name   string
		change func(snapshot)
	}{
		{"a new waiting declaration", func(s snapshot) { s.agents["foureyes"].stateAt = shellNow.Add(-time.Minute) }},
		{"a new ask id", func(s snapshot) { s.agents["master"].askID = "1793030000000-0" }},
		{"a new prompt episode", func(s snapshot) { s.agents["sentinel"].obsSince = shellNow.Add(-time.Second) }},
	} {
		before := m.sig.bells
		s := shellFixture(shellNow)
		c.change(s)
		at = at.Add(time.Second)
		m = pollAgain(t, m, s, at)
		if m.sig.bells != before+1 {
			t.Errorf("%s: %d bells, want %d", c.name, m.sig.bells, before+1)
		}
		at = at.Add(time.Second)
		if m = pollAgain(t, m, s, at); m.sig.bells != before+1 {
			t.Errorf("%s twice rang twice", c.name)
		}
	}
	off := shellWith(t, 140, 40, shellConfig{settings: busmonSettings{}}, false)
	s := shellFixture(shellNow)
	s.agents["master"].askID = "1793030000000-0"
	if off = pollAgain(t, off, s, at); off.sig.bells != 0 {
		t.Fatalf("bell = false rang %d", off.sig.bells)
	}
}

// The fade is a fadeMsg scheduled only while an agent waits, with fade on
// and a palette that has the shades; a fade frame schedules the next.
func TestShellFadeSchedule(t *testing.T) {
	pinLocal(t)
	for _, c := range []struct {
		name    string
		fade    bool
		colors  int
		waiting bool
		want    bool
	}{
		{"waiting, fade on, true colour", true, 1 << 24, true, true},
		{"fade = false", false, 1 << 24, true, false},
		{"16 colours", true, 16, true, false},
		{"nobody waits", true, 1 << 24, false, false},
	} {
		withPalette(t, "dark", false, c.colors)
		m := shellWith(t, 140, 40, shellConfig{settings: busmonSettings{Fade: c.fade}}, false)
		if !c.waiting {
			s := shellFixture(shellNow)
			for _, a := range s.agents {
				a.state, a.askID, a.openAsk, a.obsPrompt = "idle", "", 0, false
			}
			m.sig.fading = false
			m = pollAgain(t, m, s, shellNow.Add(time.Second))
		}
		if m.sig.fading != c.want {
			t.Errorf("%s: fade scheduled %v, want %v", c.name, m.sig.fading, c.want)
		}
		if c.want {
			if m = step(t, m, fadeMsg(shellNow.Add(100*time.Millisecond))); !m.sig.fading {
				t.Errorf("%s: a fade frame did not schedule the next", c.name)
			}
		}
	}
}

// The reminder opens after PopupAfter on the decisions due, never while the
// compose window is open (it opens once the window closes); Esc snoozes them
// for Snooze; Enter on a wait writes to its agent.
func TestShellReminder(t *testing.T) {
	pinLocal(t)
	set := busmonSettings{PopupAfter: 5 * time.Minute, Snooze: 15 * time.Minute}
	m := shellWith(t, 140, 40, shellConfig{settings: set}, false)
	d := m.ui.reminder
	if d == nil {
		t.Fatal("no reminder after PopupAfter")
	}
	var agents []string
	for _, it := range d.items {
		agents = append(agents, it.Agent)
		if it.Rank == rankUnread {
			t.Fatalf("an unread line in the reminder: %+v", it)
		}
	}
	// Due: foureyes' wait (6 m). Not yet: master's ask (4 m), sentinel's
	// prompt (90 s); the gate's ref is no stream id, so it has no date.
	if !strings.Contains(m.grid().plain(), "Waiting for you") || strings.Join(agents, ",") != "foureyes" {
		t.Fatalf("reminder items %q", agents)
	}
	m = keys(t, m, kEsc)
	if m.ui.reminder != nil {
		t.Fatal("Esc did not close the reminder")
	}
	// 30 s later nothing new is due (master's ask turns 5 m only at 60 s).
	if m = pollAgain(t, m, shellFixture(shellNow), shellNow.Add(30*time.Second)); m.ui.reminder != nil {
		t.Fatal("the snoozed items came back before Snooze")
	}
	m = pollAgain(t, m, shellFixture(shellNow), shellNow.Add(16*time.Minute))
	if m.ui.reminder == nil || len(m.ui.reminder.items) != 2 {
		t.Fatalf("once the snooze is over: %+v", m.ui.reminder)
	}

	// Not over the compose window: it waits for the window to close.
	c := shellWith(t, 140, 40, shellConfig{}, false)
	c.cfg.settings = set
	c = keys(t, c, 'I')
	if c = pollAgain(t, c, shellFixture(shellNow), shellNow.Add(time.Second)); c.ui.reminder != nil {
		t.Fatal("the reminder opened over the compose window")
	}
	if c = keys(t, c, kEsc); c.ui.reminder == nil {
		t.Fatal("the reminder did not open once the window closed")
	}
	// Enter on a wait writes to its agent.
	for c.ui.reminder.items[c.ui.reminder.sel].Rank != rankWait {
		c = keys(t, c, kDown)
	}
	agent := c.ui.reminder.items[c.ui.reminder.sel].Agent
	if c = keys(t, c, kEnter); c.ui.compose == nil || c.ui.compose.to.value() != "@"+agent {
		t.Fatalf("Enter on %s's wait: compose %+v", agent, c.ui.compose)
	}
}

// The recap opens by itself at the first poll when the last visit is
// AutoAfter old, as a reader page, and not when it is recent; R opens it.
func TestShellRecapAutoOpens(t *testing.T) {
	pinLocal(t)
	set := busmonSettings{AutoAfter: 8 * time.Hour}
	cache := &busmonCache{Project: "agent-bus-monitor"}
	m := shellWith(t, 140, 40, shellConfig{settings: set, priorVisit: shellNow.Add(-9 * time.Hour), cache: cache}, false)
	if p := m.rd.top(); p == nil || p.kind != ovRecap || !strings.Contains(m.grid().plain(), "Where was I?") {
		t.Fatalf("no recap after 9 h away: %+v", p)
	}
	if cache.LastVisit != shellNow.UnixMilli() {
		t.Fatalf("the visit was not recorded: %d", cache.LastVisit)
	}
	recent := shellWith(t, 140, 40, shellConfig{settings: set, priorVisit: shellNow.Add(-time.Hour)}, false)
	if recent.rd.open() {
		t.Fatal("a recap after one hour away")
	}
	if recent = keys(t, recent, 'R'); recent.rd.top() == nil || recent.rd.top().kind != ovRecap {
		t.Fatal("R did not open the recap")
	}
}

// The recap's summary row asks before it sends: n returns to the recap, y
// sends the request through the send path and the recap closes on success.
func TestShellRecapSummaryConfirmation(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, false)
	m.sig.recap = recapModel{Target: "coder", Rows: []recapRow{
		{Section: "Summary", Kind: "summary", Title: "Ask coder for a summary", Selectable: true, Key: "summary"}}}
	m.sig.recapPending = true
	m = step(t, m, fadeMsg(shellNow)) // any message: the recap opens once the person is free
	if p := m.rd.top(); p == nil || p.kind != ovRecap {
		t.Fatal("the built recap did not open")
	}
	if m = keys(t, m, kEnter); !m.ui.summary || !strings.Contains(m.grid().plain(), "Send summary request?") {
		t.Fatal("Enter on the summary row did not ask")
	}
	if m = keys(t, m, 'n'); m.ui.summary || m.rd.top().kind != ovRecap || len(fw.sent) != 0 {
		t.Fatal("n did not return to the recap")
	}
	m, cmd := pressCmd(t, keys(t, m, kEnter), 'y')
	m = run(t, m, cmd)
	if len(fw.sent) != 1 || !strings.HasPrefix(fw.sent[0], "cmd coder Summarise where the project stands") || m.rd.open() {
		t.Fatalf("y: sent %q, recap open %v", fw.sent, m.rd.open())
	}
}

// Every waiting word's fade frame keeps away from data: a fade after the bell
// is not a new fact.
func TestShellFadeRingsNothing(t *testing.T) {
	pinLocal(t)
	m := shellWith(t, 140, 40, shellConfig{settings: busmonSettings{Bell: true, Fade: true}}, false)
	before := m.sig.bells
	for i := 1; i <= 5; i++ {
		m = step(t, m, fadeMsg(shellNow.Add(time.Duration(i)*100*time.Millisecond)))
	}
	if m.sig.bells != before {
		t.Fatalf("fade frames rang %d", m.sig.bells-before)
	}
}

// An open reminder follows the poll (the person's bug: an ask answered
// elsewhere stayed until a key was pressed): an item that is no longer a
// decision leaves it at once, the selection stays on a remaining item, and
// with none left it closes without snoozing anything new.
func TestShellReminderDropsWhatIsNoLongerWaiting(t *testing.T) {
	pinLocal(t)
	set := busmonSettings{PopupAfter: 5 * time.Minute, Snooze: 15 * time.Minute}
	m := shellWith(t, 140, 40, shellConfig{settings: set}, false)
	at := shellNow.Add(16 * time.Minute)
	m = keys(t, m, kEsc)
	m = pollAgain(t, m, shellFixture(shellNow), at)
	d := m.ui.reminder
	if d == nil || len(d.items) != 2 {
		t.Fatalf("setup: reminder %+v", d)
	}
	for d.items[d.sel].Rank != rankAsk {
		m = keys(t, m, kDown)
		d = m.ui.reminder
	}
	// The ask is answered: the poll no longer lists it.
	snap := shellFixture(shellNow)
	for i := range snap.asks {
		snap.asks[i].State = "answered"
	}
	m = pollAgain(t, m, snap, at.Add(time.Second))
	if d = m.ui.reminder; d == nil || len(d.items) != 1 || d.items[0].Rank == rankAsk || d.sel != 0 {
		t.Fatalf("after the answer: reminder %+v", d)
	}
	if !strings.Contains(m.grid().plain(), "Waiting for you "+GlyphNone+" 1") {
		t.Fatalf("the popup still counts the answered ask:\n%s", m.grid().plain())
	}
	// foureyes answered too: nothing left, the popup goes.
	snap = shellFixture(shellNow)
	for i := range snap.asks {
		snap.asks[i].State = "answered"
	}
	snap.agents["foureyes"].state = "idle"
	if m = pollAgain(t, m, snap, at.Add(2*time.Second)); m.ui.reminder != nil {
		t.Fatalf("nothing waits any more, the reminder is still open: %+v", m.ui.reminder)
	}
}
