package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// pressCmd is one key press with the command it returns.
func pressCmd(t testing.TB, m Model, k any) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.Msg
	switch k := k.(type) {
	case rune:
		msg = tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)})
	case tea.Key:
		msg = tea.KeyPressMsg(k)
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func clickCmd(t testing.TB, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(Model), cmd
}

// unpaused is the fixture shell with its project pause over (an answer is
// not sent while it runs) and w as its writer.
func unpaused(t testing.TB, w, h int, fw *fakeWriter) Model {
	t.Helper()
	m := fixtureShell(t, w, h)
	m.cfg.w = fw
	snap := shellFixture(shellNow)
	snap.pause = nil
	for _, a := range snap.agents {
		a.judgedAt = shellNow
	}
	return step(t, m, pollMsg{snap: snap, at: shellNow})
}

// expansionRows are the rows the expansion added to list p.
func expansionRows(m Model, p PanelID) []string {
	var out []string
	for _, it := range m.lists.of(p).items {
		if isExpandKey(it.Key) {
			out = append(out, strings.TrimSpace(it.Line.text()))
		}
	}
	return out
}

// Enter on an Activity line expands it in place under the row, indented, up
// to half the panel; a second Enter opens it in the reader; Esc returns to
// the panel with the line still selected and expanded; Esc again collapses
// it, and so does moving the selection.
func TestShellExpandThenRead(t *testing.T) {
	pinLocal(t)
	m := inState(t, fixtureShell(t, 140, 40), "expanded")
	key := feedKey(t, m, reportID)
	rows := expansionRows(m, PanelActivity)
	if m.ui.expanded != key || len(rows) == 0 || rows[0] != "Lot 1 delivered" {
		t.Fatalf("expanded %q, rows %q", m.ui.expanded, rows)
	}
	if i := m.lists.activity.index(key); !isExpandKey(m.lists.activity.items[i+1].Key) {
		t.Fatal("the text does not follow its row")
	}
	if m.rd.open() || m.lists.activity.Selected() != key {
		t.Fatal("the first Enter opened the reader or moved the selection")
	}
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p == nil || p.kind != ovMessage || p.ev.ID != reportID || m.mode() != ModeOverlay {
		t.Fatalf("second Enter: pages %+v", m.rd.pages)
	}
	m = keys(t, m, kEsc)
	if m.rd.open() || m.lists.activity.Selected() != key || m.ui.expanded != key {
		t.Fatalf("Esc from the reader: open %v, selected %q, expanded %q", m.rd.open(), m.lists.activity.Selected(), m.ui.expanded)
	}
	if m = keys(t, m, kEsc); m.ui.expanded != "" || len(expansionRows(m, PanelActivity)) != 0 {
		t.Fatal("Esc did not collapse")
	}
	m = keys(t, m, kEnter, kDown)
	if m.ui.expanded != "" || len(expansionRows(m, PanelActivity)) != 0 {
		t.Fatal("moving the selection did not collapse")
	}
}

// The expansion stops at half the panel's height with "... enter for more".
func TestShellExpansionIsCapped(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	long := strings.Repeat("a line of the report\n\n", 60)
	m = step(t, m, tailMsg{events: []bus.Event{{ID: "1793030000000-0", Kind: "report", Agent: "coder", RKind: bus.ReportNote, Message: "long", Full: long}}})
	m = keys(t, m, '1')
	m.lists.activity.Select(feedKey(t, m, "1793030000000-0"))
	m = keys(t, m, kEnter)
	rows := expansionRows(m, PanelActivity)
	if limit := m.layout.Lower.H / 2; len(rows) != limit || rows[len(rows)-1] != "... enter for more" {
		t.Fatalf("%d rows (limit %d), last %q", len(rows), m.layout.Lower.H/2, rows[len(rows)-1])
	}
}

// A For you unread line expands in place too, and stays listed (unread)
// while it is open; collapsing it reads it, and it leaves For you.
func TestShellForYouUnreadExpands(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Rank == rankUnread })
	key := m.forYou[i].Key
	m.lists.forYou.Select(key)
	m = keys(t, m, kEnter)
	if m.ui.expanded != key || len(expansionRows(m, PanelForYou)) == 0 {
		t.Fatalf("For you: expanded %q", m.ui.expanded)
	}
	before := unreadItems(m.forYou)
	if m.lists.forYou.index(key) < 0 {
		t.Fatal("the expanded line left For you")
	}
	m = keys(t, m, kEsc)
	if m.lists.forYou.index(key) >= 0 || unreadItems(m.forYou) != before-1 {
		t.Fatalf("after the collapse: listed %v, unread %d (was %d)", m.lists.forYou.index(key) >= 0, unreadItems(m.forYou), before)
	}
}

// Every overlay that shows text is a reader page, and Esc returns from a
// page to the one that opened it, then to the panels as they were.
func TestShellReaderPages(t *testing.T) {
	pinLocal(t)
	item := func(m Model, rank int) string {
		i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Rank == rank })
		return m.forYou[i].Key
	}
	m := fixtureShell(t, 140, 40)
	for _, c := range []struct {
		name string
		do   func(Model) (Model, tea.Cmd)
		kind overlayKind
		key  string
	}{
		{"for you ask", func(m Model) (Model, tea.Cmd) { m.lists.forYou.Select(item(m, rankAsk)); return pressCmd(t, m, kEnter) }, ovAsk, m.snap.asks[0].ID},
		{"for you wait", func(m Model) (Model, tea.Cmd) {
			m.lists.forYou.Select(item(m, rankWait))
			return pressCmd(t, m, kEnter)
		}, ovAgent, "foureyes"},
		{"for you gate", func(m Model) (Model, tea.Cmd) {
			m.lists.forYou.Select(item(m, rankGate))
			return pressCmd(t, m, kEnter)
		}, ovMessage, "pr-122"},
		{"agent", func(m Model) (Model, tea.Cmd) { m = keys(t, m, kTab); return pressCmd(t, m, kEnter) }, ovAgent, ""},
		{"task", func(m Model) (Model, tea.Cmd) { m = keys(t, m, '2', kDown); return pressCmd(t, m, kEnter) }, ovTask, ""},
		{"requests", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, 'r') }, ovRequests, ""},
		{"health", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, tea.Key{Code: tea.KeyF5}) }, ovHealth, ""},
		{"help", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, '?') }, ovHelp, ""},
		{"answer", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, '!') }, ovAsk, m.snap.asks[0].ID},
	} {
		got, _ := c.do(m)
		p := got.rd.top()
		if p == nil || p.kind != c.kind || (c.key != "" && p.key != c.key) || len(p.body) == 0 {
			t.Errorf("%s: page %+v", c.name, p)
			continue
		}
		if got = keys(t, got, kEsc); got.rd.open() {
			t.Errorf("%s: Esc left the reader open", c.name)
		}
	}
	// The plan needs a loaded plan: without one, a flash.
	if got, _ := pressCmd(t, m, 'P'); got.rd.open() || !strings.Contains(got.ui.flash, "no plan") {
		t.Errorf("P without a plan: open %v, flash %q", got.rd.open(), got.ui.flash)
	}
	// A stack: the agent's page, its ask over it, Esc back to the agent, then
	// to Agents with the same row selected.
	m = keys(t, m, kTab, kDown) // master, who has an open ask
	sel := m.lists.agents.Selected()
	m = keys(t, m, kEnter, '!')
	if len(m.rd.pages) != 2 || m.rd.top().kind != ovAsk || m.mode() != ModeShellAsk {
		t.Fatalf("agent then ask: %d pages, mode %d", len(m.rd.pages), m.mode())
	}
	if m = keys(t, m, kEsc); m.rd.top().kind != ovAgent || m.rd.top().key != sel {
		t.Fatal("Esc did not return to the agent's page")
	}
	if m = keys(t, m, kEsc); m.rd.open() || m.lists.agents.Selected() != sel || m.ui.focus != PanelAgents {
		t.Fatal("Esc did not return to Agents as it was")
	}
}

// A message page reads its thread in a command; the read lands in the page,
// and a read for a page since closed is dropped.
func TestShellReaderReadsTheThread(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := unpaused(t, 140, 40, fw)
	i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Rank == rankGate })
	m.lists.forYou.Select(m.forYou[i].Key)
	m, cmd := pressCmd(t, m, kEnter)
	if cmd == nil {
		t.Fatal("a gate's page did not read its thread")
	}
	msg := cmd().(threadMsg)
	if msg.id != "pr-122" || fw.threads != 1 {
		t.Fatalf("thread read %+v", msg)
	}
	stale := msg
	m = step(t, m, msg)
	if p := m.rd.top(); !p.thread.Loaded || len(p.thread.Entries) != 1 || !strings.Contains(p.threadPlain, "THREADROOT") {
		t.Fatalf("thread not in the page: %+v", p.thread)
	}
	m = keys(t, m, kEsc)
	m.lists.forYou.Select(m.forYou[i].Key)
	m, _ = pressCmd(t, m, kEnter)
	if m = step(t, m, stale); m.rd.top().thread.Loaded {
		t.Fatal("a read for a closed page landed in the new one")
	}
}

// The reader scrolls with the keys (one row, a page, the ends) and the
// wheel (three rows), never past its body.
func TestShellReaderScrolls(t *testing.T) {
	pinLocal(t)
	m := inState(t, fixtureShell(t, 60, 20), "reader")
	p, rows := m.rd.top(), m.readerRows()
	if len(p.body) <= rows {
		t.Fatalf("body of %d lines fits %d rows: nothing to scroll", len(p.body), rows)
	}
	last := len(p.body) - rows
	for _, c := range []struct {
		k    any
		want int
	}{{kDown, 1}, {kUp, 0}, {tea.Key{Code: tea.KeyPgDown}, min(rows, last)}, {tea.Key{Code: tea.KeyHome}, 0},
		{kEnd, last}, {kDown, last}} {
		if m = keys(t, m, c.k); m.rd.top().top != c.want {
			t.Fatalf("after %v: top %d, want %d", c.k, m.rd.top().top, c.want)
		}
	}
	m = keys(t, m, tea.Key{Code: tea.KeyHome})
	r := m.readerRect()
	if m = wheelAt(t, m, r.X+5, r.Y+3, false); m.rd.top().top != 3 {
		t.Fatalf("wheel down: top %d", m.rd.top().top)
	}
	if m = wheelAt(t, m, r.X+5, r.Y+3, true); m.rd.top().top != 0 {
		t.Fatalf("wheel up: top %d", m.rd.top().top)
	}
}

// y copies the page's text and Y its thread, through tea.SetClipboard (OSC52);
// y in a panel copies the selected row.
func TestShellCopy(t *testing.T) {
	pinLocal(t)
	clip := func(cmd tea.Cmd) string {
		if cmd == nil {
			return ""
		}
		return fmt.Sprint(cmd())
	}
	m := inState(t, fixtureShell(t, 140, 40), "reader")
	m, cmd := pressCmd(t, m, 'y')
	if got := clip(cmd); !strings.Contains(got, "Lot 1 delivered") || !strings.Contains(m.ui.flash, "copied") {
		t.Fatalf("y: %q, flash %q", got, m.ui.flash)
	}
	if _, cmd = pressCmd(t, m, 'Y'); clip(cmd) == "" {
		t.Fatal("Y copied nothing")
	}
	m = keys(t, m, kEsc, kEsc, kTab, kTab) // Agents
	if _, cmd = pressCmd(t, m, 'y'); !strings.Contains(clip(cmd), m.snap.agents[m.lists.agents.Selected()].message) {
		t.Fatal("y on Agents did not copy the status message")
	}
}

// The reader's ask mode: a digit answers with that option, once (a second
// press while it is sent does nothing), then the next open question shows;
// the last answer closes the page. A paused project's answer is not sent.
func TestShellAskAnswersByKey(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := unpaused(t, 140, 40, fw)
	id := m.snap.asks[0].ID
	m = keys(t, m, '!')
	m, cmd := pressCmd(t, m, '1')
	if cmd == nil {
		t.Fatal("1 sent nothing")
	}
	if _, again := pressCmd(t, m, '1'); again != nil {
		t.Fatal("a second press sent the answer again")
	}
	m = step(t, m, cmd())
	if want := []string{"answer " + id + " go yes"}; !slices.Equal(fw.sent, want) {
		t.Fatalf("sent %q, want %q", fw.sent, want)
	}
	if p := m.rd.top(); p == nil || p.ask.q != 1 || !strings.Contains(m.ui.flash, "yes sent to master") {
		t.Fatalf("after the answer: page %+v, flash %q", p, m.ui.flash)
	}
	fw.left = 0
	m, cmd = pressCmd(t, m, '2')
	if m = step(t, m, cmd()); m.rd.open() {
		t.Fatal("the last answer left the page open")
	}
	if fw.sent[1] != "answer "+id+" tag no" {
		t.Fatalf("second answer %q", fw.sent[1])
	}

	paused := fixtureShell(t, 140, 40)
	paused.cfg.w = fw
	paused = keys(t, paused, '!')
	if _, cmd := pressCmd(t, paused, '1'); cmd != nil || len(fw.sent) != 2 {
		t.Fatal("an answer was sent while the project is paused")
	}
}

// optionAt is the screen row of option i of the ask page.
func optionAt(t testing.TB, m Model, i int) (x, y int) {
	t.Helper()
	p, r := m.rd.top(), m.readerRect()
	if i >= len(p.spans) {
		t.Fatalf("no option %d: spans %v", i, p.spans)
	}
	return r.X + 4, r.Y + 1 + p.spans[i].from - p.top
}

// A click on an option sends the same answer as its key, once, and the next
// open question shows; a click on "Other" opens the compose window for that
// question; a click (or a key) on a question already answered does nothing.
func TestShellAskAnswersByClick(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := unpaused(t, 140, 40, fw)
	id := m.snap.asks[0].ID
	m = keys(t, m, '!')
	x, y := optionAt(t, m, 1) // "no"
	m, cmd := clickCmd(t, m, x, y)
	if cmd == nil {
		t.Fatal("the click sent nothing")
	}
	if _, again := clickCmd(t, m, x, y); again != nil {
		t.Fatal("a second click sent it again")
	}
	m = step(t, m, cmd())
	if want := []string{"answer " + id + " go no"}; !slices.Equal(fw.sent, want) || m.rd.top().ask.q != 1 {
		t.Fatalf("sent %q, question %d", fw.sent, m.rd.top().ask.q)
	}
	// Back on the answered question: no options to click.
	m = keys(t, m, tea.Key{Code: tea.KeyLeft})
	if p := m.rd.top(); p.ask.q != 0 || p.spans != nil {
		t.Fatalf("answered question: q %d, spans %v", p.ask.q, p.spans)
	}
	r := m.readerRect()
	for row := r.Y + 1; row < r.Y+r.H-1; row++ {
		if _, cmd := clickCmd(t, m, r.X+4, row); cmd != nil {
			t.Fatalf("a click on row %d of an answered question sent something", row)
		}
	}
	for _, k := range []any{'1', '2', kEnter} {
		if _, cmd := pressCmd(t, m, k); cmd != nil {
			t.Fatalf("%v on an answered question sent something", k)
		}
	}
	if len(fw.sent) != 1 {
		t.Fatalf("sent %q", fw.sent)
	}
	// "Other" on the open question opens the compose window for it.
	m = keys(t, m, tea.Key{Code: tea.KeyRight})
	m.ui.askDrafts[id+"|tag"] = "a draft"
	x, y = optionAt(t, m, 2)
	m, cmd = clickCmd(t, m, x, y)
	c := m.ui.compose
	if cmd != nil || c == nil || c.kind != composeAnswer || c.askID != id || c.qid != "tag" || c.to != "master" || c.text != "a draft" {
		t.Fatalf("Other: cmd %v, compose %+v", cmd != nil, c)
	}
	if m = keys(t, m, kEsc); m.rd.open() {
		t.Fatal("Esc did not close the ask")
	}
}

// A click on the selected row expands it, a click on the expanded row (or
// its text) opens the reader, and a click on For you or Agents leaves the
// reader.
func TestShellClickExpandsThenReads(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	m = keys(t, m, '1')
	key := feedKey(t, m, reportID)
	m.lists.activity.Select(key)
	m.refresh()
	lst := &m.lists.activity
	_, iy, _, _ := lst.inner()
	row := iy + lst.index(key) - lst.top
	m = clickAt(t, m, lst.rect.X+10, row)
	if m.ui.expanded != key {
		t.Fatal("a click on the selected row did not expand it")
	}
	m = clickAt(t, m, lst.rect.X+10, row+1) // its text
	if !m.rd.open() || m.rd.top().ev.ID != reportID {
		t.Fatal("a click on the expanded text did not open the reader")
	}
	x, y := inPanel(m, PanelAgents)
	if m = clickAt(t, m, x, y); m.rd.open() || m.ui.focus != PanelAgents {
		t.Fatal("a click on Agents did not leave the reader")
	}
}

// A fade frame renders no markdown and rebuilds no page: only the waiting
// words' cells change between two consecutive frames (busmon Charm spec,
// "Caching").
func TestShellFadeCost(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", false, 1<<24)
	m := inState(t, fixtureShell(t, 140, 40), "reader") // the report in the reader, its row expanded
	if !m.rd.open() || m.ui.expanded == "" {
		t.Fatal("not reading an expanded row")
	}
	renders, builds := markdownRenders.Load(), m.cache.builds
	before := m.grid()
	m = step(t, m, fadeMsg(shellNow.Add(400*time.Millisecond))) // the clock still reads :05
	after := m.grid()
	if markdownRenders.Load() != renders || m.cache.builds != builds {
		t.Fatalf("a fade frame rendered markdown (%d) or built pages (%d)", markdownRenders.Load()-renders, m.cache.builds-builds)
	}
	changed := 0
	for y := 0; y < before.h; y++ {
		for x := 0; x < before.w; x++ {
			a, b := before.at(x, y), after.at(x, y)
			if a == b {
				continue
			}
			if a.st.Fade == 0 && b.st.Fade == 0 {
				t.Fatalf("cell %d,%d changed and is no waiting word: %q -> %q", x, y, a.text, b.text)
			}
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("the fade changed nothing: the test proves nothing")
	}
	// A poll is new data: the page is built again.
	m = step(t, m, pollMsg{snap: m.snap, at: shellNow.Add(2 * time.Second)})
	if m.cache.builds == builds {
		t.Fatal("a poll did not rebuild the page")
	}
}
