package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// View is pure (busmon Charm spec, "Architecture"): it reads only the model
// and paints it; refresh, on the update path, has already built every row.
func (m Model) View() tea.View {
	v := tea.NewView(m.grid().render(m.paint))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// grid paints the screen onto cells with the layout's geometry.
func (m Model) grid() *grid {
	g := newGrid(m.width, m.height)
	l := m.layout
	if l.Tier == TierTooSmall || m.width == 0 {
		msg := fmt.Sprintf("terminal too small (%dx%d)", m.width, m.height)
		drawLine(g, 0, m.height/2, m.width, Line{{Text: strings.Repeat(" ", max(0, (m.width-Width(msg))/2)) + msg, Role: RoleAttention}}, false)
		return g
	}
	header := headerRows(m.headerData(), m.width, l.Tier, l.Header.H)
	for i := 0; i < l.Header.H && i < len(header); i++ {
		drawLine(g, 0, l.Header.Y+i, m.width, header[i], false)
	}
	if l.Tabs.H > 0 {
		drawLine(g, 0, l.Tabs.Y, m.width, tabBar(narrowPanels, l.Active, m.width), false)
	}
	reading := m.rd.open()
	for _, p := range l.Visible {
		if reading && (p == l.Active || l.Tier == TierNarrow) {
			continue // the reader has the lower zone (narrow: the view)
		}
		lst := *m.lists.of(p) // drawn from a copy: View leaves the model as it found it
		lst.draw(g)
	}
	if reading {
		m.drawReader(g)
	} else if l.Input.H > 0 {
		drawLine(g, l.Input.X, l.Input.Y, l.Input.W, m.inputRow(l.Input.W), false)
		if row := m.completionRow(l.Input.W); row != nil && l.Input.Y > 0 { // over the row above
			drawLine(g, l.Input.X, l.Input.Y-1, l.Input.W, row, false)
		}
	}
	footer := footerHints(m.mode(), m.ui.focus, l.Tier, m.width)
	if m.ui.flash != "" {
		role := RoleOK
		if m.ui.flashErr {
			role = RoleError
		}
		footer = FitLine(Line{{Text: " " + m.ui.flash, Role: role}}, m.width)
	}
	drawLine(g, 0, l.Footer.Y, m.width, footer, false)
	// The window and the dialogs over everything, footer included; the
	// pause popup on top.
	if m.ui.compose != nil {
		m.drawCompose(g)
	}
	switch {
	case m.ui.reminder != nil:
		m.drawReminder(g)
	case m.ui.summary:
		m.drawSummary(g)
	}
	if d := m.ui.pausePopup; d != nil {
		drawPausePopup(g, m.pauseDialogRect(), d.rec, d.sel, m.now)
	}
	return g
}

// headerData is the header's facts from the snapshot. The shell's badges are
// the For you subsets: decisions, and the unread lines.
func (m Model) headerData() headerData {
	s := m.snap
	mineReqs, mineThreads := forPerson(s.requests, s.threads, m.cfg.self)
	return headerData{project: m.cfg.project, health: s.health, guardian: s.guardian, dispatch: s.dispatch,
		missions: s.missions, pilot: s.pilot, self: m.cfg.self, update: s.update, note: s.configNote,
		waiting: waitingText(mineReqs, mineThreads, m.now), forYou: len(decisions(m.forYou)),
		unread: unreadItems(m.forYou), pause: s.pause, holds: s.holds, pace: s.pace, budgets: s.budgets,
		plan: s.plan, now: m.now, shell: true}
}

// tabBar is "1 Activity  2 Board  3 Requests" with the active tab
// highlighted; names shorten when they do not
// fit width.
func tabBar(tabs []PanelID, active PanelID, width int) Line {
	label := func(i int, p PanelID, short bool) string {
		name := panelNames[p]
		if p == PanelForYou {
			name = "For you"
		}
		if short {
			name = map[PanelID]string{PanelForYou: "You", PanelAgents: "Agt", PanelActivity: "Act", PanelBoard: "Brd", PanelRequests: "Req"}[p]
		}
		return fmt.Sprintf(" %d %s ", i+1, name)
	}
	short := false
	total := 0
	for i, p := range tabs {
		total += Width(label(i, p, false))
	}
	short = total > width
	var l Line
	for i, p := range tabs {
		// Every tab in its panel's colour (A4: a muted one vanished beside the
		// focused pill); the active one is bold, and the frame pills it.
		l = append(l, Seg{Text: label(i, p, short), Role: panelHue[p], Bold: p == active, Reverse: p == active && th.Palette() == PaletteNone})
	}
	return l
}

// fillLists gives every list its place, focus, rows and frame.
func (m *Model) fillLists() {
	l, s, now := m.layout, m.snap, m.now
	rowW := func(r Rect) int { return max(0, r.W-2-listGutter) }
	for _, p := range narrowPanels {
		lst := m.lists.of(p)
		lst.rect, lst.focused = l.rect(p), m.ui.focus == p
	}

	// For you: every item; at the medium tier, unfocused, four and "+N more".
	fy := &m.lists.forYou
	items := m.forYou
	more := 0
	if l.Tier == TierMedium && m.ui.focus != PanelForYou && len(items) > forYouMediumRows {
		more, items = len(items)-forYouMediumRows, items[:forYouMediumRows]
	}
	rows := make([]ListItem, 0, len(items)+1)
	for _, it := range items {
		rows = append(rows, ListItem{Key: it.Key, Line: forYouRow(it, rowW(l.ForYou), now), Selectable: true})
	}
	if more > 0 {
		rows = append(rows, ListItem{Key: "more", Line: Line{{Text: fmt.Sprintf("+%d more", more), Role: RoleMuted}}, Selectable: true})
	}
	if len(rows) == 0 {
		rows = []ListItem{{Line: Line{{Text: "nothing waits for you", Role: RoleMuted}}}}
	}
	fy.SetItems(m.withExpansion(PanelForYou, rows, rowW(l.ForYou)))
	n := len(decisions(m.forYou))
	fy.SetFrame(Line{{Text: "For you", Role: RoleText}},
		Line{{Text: fmt.Sprintf("%d for you %s %d unread", n, GlyphNone, unreadItems(m.forYou)), Role: RoleMuted}})

	// Agents: today's rows and sort.
	ag := &m.lists.agents
	names, hidden := visibleAgents(s.agents, s.pilot, m.ui.showAll, now)
	agentItems := make([]ListItem, 0, len(names))
	working, blocked := 0, 0
	for _, name := range names {
		a := s.agents[name]
		agentItems = append(agentItems, ListItem{Key: name, Line: agentRow(name, a, name == s.pilot, l.Tier, rowW(l.Agents), now), Selectable: true})
		switch _, word, _, _ := stateLook(a); word {
		case "working":
			working++
		case "blocked":
			blocked++
		}
	}
	if len(agentItems) == 0 {
		agentItems = []ListItem{{Line: Line{{Text: "no agent has published yet", Role: RoleMuted}}}}
	}
	ag.SetHeader(agentHeader(l.Tier, rowW(l.Agents)))
	ag.SetItems(agentItems)
	if !m.ui.agentsTouched && len(names) > 0 {
		ag.First() // the top (most urgent) agent, until the person moves
	}
	ag.viewport()
	ag.SetFrame(Line{{Text: "Agents", Role: RoleText}}, Line{{Text: agentsLabel(len(names), working, blocked, hidden, m.ui.showAll, ag.Scroll()), Role: RoleMuted}})

	// The tabbed panel's three lists.
	lower := rowW(l.Lower)
	act := &m.lists.activity
	var feedItems []ListItem
	admitted, events := admittedEvents(m.feed.feed, m.ui.filter)
	for _, fl := range m.feed.feed {
		if m.ui.filter.match(fl) {
			feedItems = append(feedItems, ListItem{Key: fl.id, Line: activityRowAt(fl, l.Tier, lower, now), Selectable: !fl.sep})
		}
	}
	act.SetHeader(activityHeader(l.Tier, lower))
	act.SetItems(m.withExpansion(PanelActivity, feedItems, lower))
	m.ui.pause.Recount(admitted)
	at, al := activityFrame(&m.ui.pause, len(admitted), events, m.ui.filter)
	act.SetFrame(m.lowerTitle(PanelActivity, at), al)

	bd := &m.lists.board
	bd.SetHeader(boardHeader(lower))
	bd.SetItems(boardItems(s.board, now, lower))
	bt, bl := boardFrame(s.board)
	bd.SetFrame(m.lowerTitle(PanelBoard, bt), bl)

	rq := &m.lists.requests
	rq.SetItems(requestItems(s.requests, s.threads, now, lower))
	rq.SetFrame(m.lowerTitle(PanelRequests, Line{{Text: "Requests", Role: RoleText}}),
		requestsLabel(s.requests, s.threads, m.cfg.self, openQuestionCount(s.asks)))

	m.keepInView()
	for _, p := range narrowPanels {
		m.lists.of(p).viewport()
	}
}

// lowerTitle is the tabbed panel's title: its tab bar, then what follows the
// panel's name in its own title (Activity's auto-scroll state). The narrow
// tier has its tab bar above the view: the title is the panel's own.
func (m *Model) lowerTitle(p PanelID, own Line) Line {
	if m.layout.Tier == TierNarrow {
		return own
	}
	t := tabBar(lowerTabs, p, m.layout.Lower.W-4)
	if len(own) > 1 {
		t = append(t, own[1:]...)
	}
	return t
}

// forYouKinds are the For you row's mark and word by rank.
var forYouKinds = map[int]struct {
	glyph, word string
	role        Role
}{
	rankAsk:    {"?", "ask", RoleAttention},
	rankWait:   {GlyphHuman, "wait", RoleAttention},
	rankGate:   {GlyphBlocked, "gate", RoleDispatch},
	rankUnread: {GlyphDotFull, "unread", RoleAccent},
}

// forYouRow is one For you item in exactly width cells: its mark and kind,
// the agent, the text, and its age on the right.
func forYouRow(it attentionItem, width int, now time.Time) Line {
	head, text, age := forYouParts(it, now)
	l := append(head, FitLine(text, max(0, width-head.Width()-age.Width()))...)
	return FitLine(append(l, age...), width)
}

// forYouAgeCells is the room an age takes in a row: a space and up to four
// cells ("59m", "23h", "12d").
const forYouAgeCells = 5

// forYouNatural is a row's width uncut: what the wide tier sizes For you by.
// The age counts as its fixed room, never its text, so the split does not
// move when "9m" becomes "10m" (b1 review, foureyes).
func forYouNatural(it attentionItem) int {
	head, text, _ := forYouParts(it, time.Time{})
	w := head.Width() + text.Width()
	if !it.At.IsZero() {
		w += forYouAgeCells
	}
	return w
}

func forYouParts(it attentionItem, now time.Time) (head, text, age Line) {
	k := forYouKinds[it.Rank]
	if it.Unread { // an ask whose line is unread: the row carries the mark
		k.glyph, k.role = GlyphDotFull, forYouKinds[rankUnread].role
	}
	t := Preview(it.Text)
	switch it.Rank {
	case rankAsk:
		if it.Count > 1 {
			t += fmt.Sprintf(" (%d questions)", it.Count)
		}
	case rankGate:
		if it.By != "" {
			t = "by " + Preview(it.By) + "  " + t
		}
	}
	if !it.At.IsZero() {
		age = Line{{Text: " " + humanAge(now.Sub(it.At)), Role: RoleMuted}}
	}
	head = Line{col(k.glyph, 2, k.role), col(k.word, 7, k.role), col(Preview(it.Agent), 10, RoleText)}
	return head, Line{{Text: t, Role: RoleText}}, age
}
