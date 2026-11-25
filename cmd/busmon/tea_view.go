package main

import (
	"cmp"
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"slices"
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
	if m.ui.idle { // any motion, or coming back to the window, wakes it (idle view §3)
		v.MouseMode, v.ReportFocus = tea.MouseModeAllMotion, true
	}
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
	if m.ui.idle { // the idle view instead of the dashboard (idleview_draw.go)
		return m.idleGrid()
	}
	header := headerRows(m.headerData(), m.width, l.Tier, l.Header.H)
	for i := 0; i < l.Header.H && i < len(header); i++ {
		drawLine(g, 0, l.Header.Y+i, m.width, header[i], false)
	}
	if b := l.Banner; b.H > 0 {
		line := m.bannerLine(b.W)
		if m.snap.pause != nil {
			line = m.pauseBannerLine(b.W)
		}
		drawLine(g, b.X, b.Y, b.W, line, false)
	}
	if l.Tabs.H > 0 && l.Active != PanelRequests { // the Requests view has its own title, not the tabs
		drawLine(g, 0, l.Tabs.Y, m.width, tabBarNotes(narrowPanels, m.tabOf(l.Active), m.width, true, m.tabNotes()), false)
	}
	reading := m.rd.open()
	drawer := reading && l.Tier == TierWide && m.rd.pages[0].drawer
	for _, p := range l.Visible {
		if reading && ((p == l.Active && !(drawer && l.Lower.H > 0)) || l.Tier == TierNarrow) {
			continue // the reader has the lower zone (narrow: the view); a drawer sized to its content leaves it the active tab (§4.2)
		}
		if drawer && p == PanelAgents {
			continue // the plan drawer is under For you, over Agents (views-redesign §4.1)
		}
		lst := *m.lists.of(p) // drawn from a copy: View leaves the model as it found it
		lst.draw(g)
		if p == PanelPlan {
			if br := lst.BottomRect(); br.H > 0 { // the selected task's detail under the list (views-redesign §4.1)
				drawPlanDetail(g, br.X+1, br.Y, br.W-2, m.planTabDetail(br.W-2)) // one cell each side, as the drawer's body
			}
		}
	}
	if reading {
		m.drawReader(g)
	}
	if l.Input.H > 0 && (!reading || m.ui.line == lineMessage) { // Ctrl+P on a page: the line over it
		drawLine(g, l.Input.X, l.Input.Y, l.Input.W, m.inputRow(l.Input.W), false)
		if row := m.completionRow(l.Input.W); row != nil && l.Input.Y > 0 { // over the row above
			drawLine(g, l.Input.X, l.Input.Y-1, l.Input.W, row, false)
		}
		rows := m.paletteLines(l.Input.W, l.Input.Y) // the palette's list, ending right above the line
		for i, row := range rows {
			drawLine(g, l.Input.X, l.Input.Y-len(rows)+i, l.Input.W, row, false)
		}
	}
	footer := footerHintsOn(m.mode(), m.ui.focus, l.Tier, m.width, m.pageOffers, m.pageHint)
	if m.ui.flash != "" {
		role := RoleOK
		if m.ui.flashErr {
			role = RoleError
		}
		footer = FitLine(Line{{Text: " " + m.ui.flash, Role: role}}, m.width)
	} else if n := m.noticeLine(m.width); n != nil {
		footer = m.footerWithNotice(n)
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
	}
	if m.ui.tour != nil {
		m.drawTour(g)
	}
	if m.ui.del != nil {
		m.drawDeletePanel(g)
	}
	if d := m.ui.pausePopup; d != nil {
		drawPausePopup(g, m.pauseDialogRect(), d.rec, d.sel, m.now)
	}
	if m.ui.dialog != nil { // last: it dims everything under it
		drawConfirmDialog(g, m.ui.dialog, m.width, m.height)
	}
	return g
}

// headerData is the header's facts from the snapshot. The shell's strip is a
// partition of For you (busmon-queue-triage §3).
func (m Model) headerData() headerData {
	s := m.snap
	_, _, between := m.lists.reqView.titleCounts()
	return headerData{icons: m.cfg.settings.Icons, dev: m.cfg.settings.Dev, project: m.cfg.project, badge: m.now.Before(m.ui.badgeUntil), health: s.health, guardian: s.guardian, dispatch: s.dispatch,
		missions: s.missions, pilot: s.pilot, self: m.cfg.self, update: s.update, codex: s.codexUpdate, note: s.configNote,
		between: between, background: backgroundChip(s.agents, s.pilot, m.now), strip: m.work.strip(activityUnread(m.feed.feed)),
		unread: activityUnread(m.feed.feed), pause: s.pause, holds: s.holds, pace: s.pace, budgets: s.budgets, bars: m.barView(),
		plan: s.plan, now: m.now, shell: true, others: othersSummary(m.others, m.cfg.project, time.Now())}
}

// tabBar is "1 Activity  2 Board  3 Requests" with the active tab
// highlighted; names shorten when they do not
// fit width.
func tabBar(tabs []PanelID, active PanelID, width int, focused bool) Line {
	return tabBarNotes(tabs, active, width, focused, nil)
}

// tabNotes are the tab bar's notes after a tab's name: what came into
// Activity while it was not shown, "3 new" (plan-tab-r3 item 1).
func (m *Model) tabNotes() map[PanelID]string {
	if m.feed == nil {
		return nil
	}
	n := 0
	for i := len(m.feed.feed) - 1; i >= 0 && m.feed.feed[i].seq > m.ui.activitySeen; i-- {
		if !m.feed.feed[i].sep {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return map[PanelID]string{PanelActivity: fmt.Sprintf("%d new", n)}
}

// tabBarNotes is tabBar with a note after an inactive tab's name, " · 3
// new"; the notes go first when the bar is too wide, before any name
// shortens.
func tabBarNotes(tabs []PanelID, active PanelID, width int, focused bool, notes map[PanelID]string) Line {
	if active == PanelRequests { // a caller that names no origin (tabOf does): Work's
		active = PanelWork
	}
	short := map[PanelID]string{PanelForYou: "You", PanelAgents: "Agt", PanelActivity: "Act", PanelBoard: "Brd", PanelRequests: "Req", PanelWork: "Wrk", PanelPlan: "Pln"}
	names := make([]string, len(tabs))
	for i, p := range tabs {
		names[i] = panelLabel(p)
	}
	label := func(i int) string { // the block, a blank cell each side
		if n := notes[tabs[i]]; n != "" && tabs[i] != active {
			return fmt.Sprintf(" %d %s %s %s ", i+1, names[i], GlyphNone, n)
		}
		return fmt.Sprintf(" %d %s ", i+1, names[i])
	}
	total := func() int {
		n := 0
		for i := range tabs {
			n += Width(label(i)) + 1 // and the blank column after it
		}
		return n
	}
	if total() > width { // too wide: the notes go first
		notes = nil
	}
	// Too wide: shorten the longest name first, one at a time, until the
	// bar fits (A11's sixth view must not shorten every label).
	for total() > width {
		longest := -1
		for i, p := range tabs {
			if names[i] != short[p] && (longest < 0 || Width(names[i]) > Width(names[longest])) {
				longest = i
			}
		}
		if longest < 0 {
			break
		}
		names[longest] = short[tabs[longest]]
	}
	// tab-style blocks: each label " 1 Activity " is a block, padded by a
	// blank cell on each side in its own colour (busmon-tab-pad, the person),
	// then one blank column before the next. The active tab is a pill in its panel's colour,
	// dimmed like the frame when the panel has not the focus; an inactive
	// one is grey on the tab background (plain text without true colour).
	var l Line
	for i, p := range tabs {
		block := label(i)
		if p == active {
			fade := 0
			if !focused {
				fade = frameFade
			}
			l = append(l, Seg{Text: block, Role: panelHue[p], Bold: true, Pill: true, Fade: fade, Tab: true})
		} else {
			l = append(l, Seg{Text: block, Role: RoleMuted, Off: true, Tab: true})
		}
		l = append(l, Seg{Text: " ", Role: RoleText, Tab: true})
	}
	return l
}

// tabLabels are the tab bar's labels, one per tab (its block and its blank
// column): the spans a click hits.
func tabLabels(tabs []PanelID, active PanelID, width int, notes map[PanelID]string) []string {
	l := tabBarNotes(tabs, active, width, true, notes)
	out := make([]string, 0, len(tabs))
	for i := 0; i+1 < len(l); i += 2 {
		out = append(out, l[i].Text+l[i+1].Text)
	}
	return out
}

// fillLists gives every list its place, focus, rows and frame.
func (m *Model) fillLists() {
	l, s, now := m.layout, m.snap, m.now
	rowW := func(r Rect) int { return max(0, r.W-2-listGutter) }
	for _, p := range shellPanels {
		lst := m.lists.of(p)
		lst.rect, lst.focused = l.rect(p), m.ui.focus == p
	}

	// For you: every item but the unread lines (home-inbox-drawer §3.2); at
	// the medium tier, unfocused, four and "+N more"; then the plan pointer.
	fy := &m.lists.forYou
	items := forYouPanelItems(m.forYou)
	pauseRow, paused := m.forYouPauseRow()
	shown := forYouMediumRows
	if paused { // the pause row is one of the four
		shown--
	}
	more := 0
	if l.Tier == TierMedium && m.ui.focus != PanelForYou && len(items) > shown {
		more, items = len(items)-shown, items[:shown]
	}
	rows := make([]ListItem, 0, len(items)+1)
	for _, it := range items {
		rows = append(rows, ListItem{Key: it.Key, Line: forYouRow(it, rowW(l.ForYou), now), Selectable: true})
	}
	if more > 0 {
		rows = append(rows, ListItem{Key: "more", Line: Line{{Text: fmt.Sprintf("+%d more", more), Role: RoleMuted}}, Selectable: true})
	}
	if len(rows) == 0 { // say what is expected, if anything, then the plan pointer
		for _, r := range forYouEmptyRows(s, m.lists.reqView.count(secOwed), m.work.decideCount()) {
			rows = append(rows, ListItem{Key: r.key, Line: r.line, Selectable: r.key != ""})
		}
	} else if p, ok := planPointer(s); ok { // below the decisions, muted, never counted (§3.3)
		rows = append(rows, ListItem{Key: p.key, Line: p.line, Selectable: true})
	}
	if paused { // the project paused: For you's first line (pausebanner.go)
		rows = append([]ListItem{pauseRow}, rows...)
	}
	fy.SetItems(rows)
	fy.SetFrame(Line{{Text: "For you", Role: RoleText}}, Line{{Text: forYouLabel(m.work.strip(0)), Role: RoleMuted}})

	// Agents: today's rows and sort.
	ag := &m.lists.agents
	names, hidden := visibleAgents(s.agents, s.pilot, m.ui.showAll, now)
	agentItems := make([]ListItem, 0, len(names))
	working, blocked := 0, 0
	for _, name := range names {
		a := s.agents[name]
		agentItems = append(agentItems, ListItem{Key: name, Line: agentRow(m.cfg.settings.Icons, name, a, name == s.pilot, l.Tier, rowW(l.Agents), now), Selectable: true})
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
	ag.SetHeader(agentHeader(m.cfg.settings.Icons, l.Tier, rowW(l.Agents)))
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
			feedItems = append(feedItems, ListItem{Key: fl.id, Line: m.feed.rows.row(fl, l.Tier, lower, now), Selectable: !fl.sep})
		}
	}
	m.feed.rows.sweep()
	act.SetHeader(activityHeader(l.Tier, lower))
	act.SetItems(m.withExpansion(PanelActivity, feedItems, lower))
	if seen, left := act.holdSelection(m.ui.expandIn == PanelActivity && m.ui.expanded != "" && m.ui.expanded == act.Selected()); left { // new lines or a smaller frame: the selection stays in view
		at := m.feed.seq
		if fl, ok := m.feedLine(seen); ok {
			at = fl.seq // what came after the last line in view counts as new
		}
		m.ui.pause.Pause(at)
	}
	if act.JumpPending() { // follow off, End/G or the start: what comes after the jump is new
		m.ui.pause.Resume()
		m.ui.pause.Pause(m.feed.seq)
	}
	m.ui.pause.Recount(admitted)
	at, al := activityFrame(&m.ui.pause, len(admitted), events, m.ui.filter)
	act.SetFrame(m.lowerTitle(PanelActivity, at), al)

	bd := &m.lists.board
	bv := boardViewOf(s.board, now, lower, boardOpts{noAccept: m.cfg.settings.NoAcceptAfter, noReply: m.cfg.settings.NoReplyAfter,
		showDone: m.ui.boardDone, masterDown: masterDown(s.agents, cmp.Or(s.pilot, "master")), between: m.ui.boardBetween, self: m.cfg.self, agents: s.agents})
	bd.SetHeader(bv.header)
	bd.SetItems(bv.items)
	bd.SetFrame(m.lowerTitle(PanelBoard, Line{{Text: "Board", Role: RoleText}}), bv.label)
	bd.SetFooter(bv.footer)

	m.fillPlanTab(lower) // the roadmap, the fourth tab (views-redesign §4)
	m.fillReleases()     // its Releases column (plan-tab-r3 item 5)

	wk := &m.lists.work // who owes what (requests-plan-redesign, layout A)
	compact := lower < workCompactW
	wd := workDraw{requests: s.requests, now: now, width: lower}
	wk.kanban = nil
	if workKanbanOn(m.cfg.settings.WorkLayout, m.width) { // layout B: the same model as columns
		v := workKanban(m.work, wd)
		v.level, v.col = m.ui.workLevel, m.ui.workCol
		if v.level == workAtCards { // in the cards the column is the selected card's, however it was reached (4, a chip, a click; foureyes)
			if c, _ := v.cardPlace(wk.Selected()); c >= 0 {
				v.col = c
			}
		}
		wk.kanban = &v
		wk.SetItems(v.items())
	} else {
		wk.SetItems(workRows(m.work, wd))
	}
	wk.SetFrame(m.lowerTitle(PanelWork, Line{{Text: "Work", Role: RoleText}}), Line{{Text: workLabel(m.work, compact), Role: RoleMuted}})

	rq := &m.lists.requests
	rv := m.lists.reqView // refresh computed it before the layout
	rq.SetItems(requestRows(rv, m.ui.reqOpen, now, lower))
	// The Requests sub-view, every thread (r, F2; views-redesign §3, (a)):
	// drawn under the tab it was opened from, where esc returns.
	// Its own title, not the tab bar (busmon-three-fixes 3): a full view, as
	// For you and Agents are titled; the tab it came from is the way back.
	rq.SetFrame(Line{{Text: "Requests", Role: RoleText}, {Text: " " + GlyphArrowR + " all threads, esc back to " + panelNames[m.reqOrigin()], Role: RoleMuted}}, requestsTitle(rv, lower))

	m.keepInView()
	for _, p := range shellPanels {
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
	var suffix Line
	if len(own) > 1 {
		suffix = own[1:]
	}
	// The tabs get what the suffix leaves, so a name shortens rather than
	// being cut by the frame (reviewer-codex).
	// Inside Work's Kanban columns the focus is below the tabs: the pill
	// fades as for an unfocused panel (busmon-work-focus).
	inside := p == PanelWork && m.lists.work.kanban != nil && m.ui.workLevel != workAtTabs
	focused := m.ui.focus == p && !inside
	if p == PanelRequests { // the sub-view's tab is its origin's
		p = m.reqOrigin()
	}
	t := tabBarNotes(lowerTabs, p, m.layout.Lower.W-4-suffix.Width(), focused, m.tabNotes())
	return append(t, suffix...)
}

// forYouKinds are the For you row's mark and word by rank.
var forYouKinds = map[int]struct {
	glyph, word string
	role        Role
}{
	rankAsk:    {"?", verbAnswer, RoleAttention}, // the Work model's verbs (correction 1)
	rankHealth: {GlyphBlocked, verbCheck, RoleAttention},
	rankWait:   {GlyphHuman, verbAnswer, RoleAttention},
	rankGate:   {GlyphGate, verbReview, RoleDispatch},
	rankReport: {GlyphDotFull, verbAnswer, RoleAttention}, // read for a --needs read report (forYouParts)
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
	if it.Rank == rankReport && it.Needs == bus.NeedsRead {
		k.word = verbRead
	}
	t := Preview(it.Text)
	switch { // a --needs report folded into this item (triage-dedupe §3)
	case it.Rank == rankReport:
	case it.Needs == bus.NeedsAnswer:
		t += " " + GlyphNone + " needs your answer"
	case it.Needs == bus.NeedsRead:
		t += " " + GlyphNone + " to read"
	}
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

// reqOrigin is the lower tab the Requests sub-view was opened from (r, F2,
// views-redesign §3, (a)); Work when none was recorded.
func (m *Model) reqOrigin() PanelID {
	if slices.Contains(lowerTabs, m.ui.reqFrom) {
		return m.ui.reqFrom
	}
	return PanelWork
}

// tabOf is the tab a panel is drawn under: the sub-view's is its origin's.
func (m *Model) tabOf(p PanelID) PanelID {
	if p == PanelRequests {
		return m.reqOrigin()
	}
	return p
}
