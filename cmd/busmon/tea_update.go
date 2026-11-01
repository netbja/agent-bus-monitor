package main

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Navigation and the lists' mouse (busmon Charm spec, step b2): keys go
// through keys.go's tables (ModeShell, ModeFilter), the lists keep
// listState's rules, and every change ends in refresh.

// mode is who owns the keyboard in the shell: the compose window, the input
// line, the reader (its ask mode), else the panels. The dialogs take their
// keys before any table (key).
func (m *Model) mode() Mode {
	switch {
	case m.ui.tour != nil:
		return ModeTour
	case m.ui.compose != nil:
		return ModeCompose
	case m.ui.line == lineFilter:
		return ModeFilter
	case m.ui.line == lineMessage:
		return ModeInput
	case m.rd.open() && m.rd.top().kind == ovAsk:
		return ModeShellAsk
	case m.rd.open():
		return ModeOverlay
	}
	return ModeShell
}

// key performs a key press. Ctrl-C quits everywhere; an open dialog, a
// pending choice or removal takes the key first.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := keyOfTea(msg)
	if k.Code == keyCtrlC {
		return tea.Quit
	}
	var cmd tea.Cmd
	switch {
	case m.ui.pausePopup != nil:
		cmd = m.pauseKey(k)
	case m.ui.tour != nil:
		cmd = m.tourKey(k)
	case m.ui.summary:
		cmd = m.summaryKey(k)
		if !m.ui.summary && !m.ui.sending {
			m.ui.summarySent = false
		}
	case m.ui.reminder != nil:
		cmd = m.reminderKey(k)
	case m.ui.askChoose != nil:
		cmd = m.chooseKey(k)
	case m.ui.confirm != nil:
		cmd = m.confirmKey(k)
	default:
		cmd = m.modeKey(k)
	}
	m.syncPause()
	m.refresh()
	return cmd
}

// modeKey is a key for whoever owns the keyboard (mode).
func (m *Model) modeKey(k Key) tea.Cmd {
	mode := m.mode()
	act := lookup(mode, m.ui.focus, m.layout.Tier, k)
	if act == ActQuit {
		return tea.Quit
	}
	if act != ActNone || mode == ModeCompose || mode == ModeInput {
		m.ui.flash, m.ui.flashErr = "", false
	}
	switch mode {
	case ModeCompose:
		return m.composeKey(act, k)
	case ModeInput, ModeFilter:
		return m.lineKey(act, k)
	}
	if m.rd.open() {
		return m.readerKey(act, k)
	}
	return m.panelKey(act)
}

// panelKey is a key on the panels (ModeShell).
func (m *Model) panelKey(act Action) tea.Cmd {
	l := m.lists.of(m.ui.focus)
	var cmd tea.Cmd
	if m.ui.focus == PanelAgents && movesSelection(act) {
		m.ui.agentsTouched = true
	}
	switch act {
	case ActNextPanel, ActPrevPanel:
		m.cyclePanel(act == ActNextPanel)
	case ActTab1, ActTab2, ActTab3, ActTab4:
		m.switchTab(int(act - ActTab1))
	case ActTabNext, ActTabPrev:
		m.stepTab(act == ActTabNext)
	case ActTab5:
		m.switchTab(4)
	case ActTab6:
		m.switchTab(5)
	case ActUp:
		l.Move(-1)
	case ActDown:
		l.Move(1)
	case ActPageUp:
		l.Move(-max(1, l.rows()))
	case ActPageDown:
		l.Move(max(1, l.rows()))
	case ActFirst:
		l.First()
	case ActLast:
		l.Last()
	case ActLive:
		m.lists.activity.Resume()
	case ActFold:
		m.ui.reqOpen.folded = !m.ui.reqOpen.folded
	case ActOpen:
		cmd = m.openTracked(m.ui.focus, false)
	case ActCopy:
		cmd = m.copySelected()
	case ActRequests:
		cmd = m.push(page{kind: ovRequests})
	case ActServices:
		cmd = m.servicesCmd(0)
	case ActHealth:
		cmd = m.push(page{kind: ovHealth})
	case ActHelp:
		cmd = m.push(page{kind: ovHelp})
	case ActPlan:
		cmd = m.pushPlan()
	case ActTokens:
		cmd = m.push(page{kind: ovTokens})
	case ActAsk:
		agent := ""
		if sel := m.lists.agents.Selected(); m.ui.focus == PanelAgents && m.snap.agents[sel] != nil && m.snap.agents[sel].openAsk > 0 {
			agent = sel
		}
		cmd = m.pushAskOf(agent)
	case ActAskOrWrite:
		cmd = m.askOrWrite()
	case ActFollow:
		if act := &m.lists.activity; act.Live() {
			act.Pause()
			m.ui.flash = "auto-scroll off: f or end resumes"
		} else {
			act.Resume()
			m.ui.flash = "auto-scroll on"
		}
	case ActNextUnread:
		m.focusPanel(PanelActivity)
		m.refresh()
		if !m.nextUnread() {
			m.ui.flash = "nothing unread"
		}
	case ActShowAll:
		m.ui.showAll = !m.ui.showAll
	case ActFilter:
		if m.ui.line == lineMessage {
			m.leaveLine(true)
		}
		m.openLine(lineFilter, m.ui.filter.raw)
	case ActCompose:
		if m.composeHasDraft() { // a window draft is never flattened into the line
			m.openMessageCompose("", "")
		} else {
			m.openLine(lineMessage, m.ui.msgDraft)
		}
	case ActComposeAt:
		m.openLine(lineMessage, "@")
	case ActComposeOpen:
		to := ""
		if m.ui.focus == PanelAgents {
			to = m.lists.agents.Selected()
		}
		line := ""
		if !m.composeHasDraft() {
			line, m.ui.msgDraft = strings.TrimSpace(m.ui.msgDraft), ""
		}
		m.openMessageCompose(line, to)
	case ActDelete:
		m.askRemove()
	case ActRecap:
		cmd = m.requestRecap(false)
	case ActEscape:
		m.escape()
	}
	return cmd
}

// open is Enter (or a click on the selected row) in list p: an Activity line,
// or a For you unread line, expands in place, then opens in the reader; an
// agent, a task, a request, an ask, a wait or a gate opens its page.
func (m *Model) open(p PanelID) tea.Cmd {
	key := m.lists.of(p).Selected()
	if key == "" {
		return nil
	}
	s := m.snap
	switch p {
	case PanelActivity:
		fl, ok := m.feedLine(key)
		if !ok {
			return nil
		}
		if m.ui.expanded == key && m.ui.expandIn == p {
			return m.pushMessage(fl.ev, fl.at)
		}
		m.expand(p, key)
	case PanelForYou:
		i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Key == key })
		if i < 0 {
			return nil
		}
		it := m.forYou[i]
		switch it.Rank {
		case rankAsk:
			return m.pushAsk(it.AskID)
		case rankWait:
			return m.pushAgent(it.Agent)
		case rankHealth: // the stall guard: F5 says what and why
			return m.push(page{kind: ovHealth})
		case rankGate: // the challenge thread: its ref is the root
			ref := strings.TrimPrefix(key, "gate:"+it.Agent+":")
			return m.pushMessage(bus.Event{ID: ref, Kind: "cmd", Type: bus.CmdChallenge, From: it.By, Target: it.Agent, Message: it.Text}, it.At)
		case rankUnread:
			if m.ui.expanded == key && m.ui.expandIn == p {
				if fl, ok := m.expandedLine(); ok {
					return m.pushMessage(fl.ev, fl.at)
				}
				return nil
			}
			m.expand(p, key)
		}
	case PanelAgents:
		return m.pushAgent(key)
	case PanelBoard:
		if _, ok := s.board[key]; ok {
			return m.push(page{kind: ovTask, key: key})
		}
	case PanelPlan:
		if key == "toggle" {
			m.ui.planExpanded = true
			m.refresh()
			return nil
		}
		if task, ok := strings.CutPrefix(key, "task:"); ok {
			return m.push(page{kind: ovPlanTask, key: task})
		}
	case PanelRequests:
		return m.openRequestRow(key)
	}
	return nil
}

// feedLine is the Activity line of a list key.
func (m *Model) feedLine(key string) (feedLine, bool) {
	for _, fl := range m.feed.feed {
		if fl.id == key && !fl.sep {
			return fl, true
		}
	}
	return feedLine{}, false
}

// copySelected is y in a panel: the selected row's text.
func (m *Model) copySelected() tea.Cmd {
	key := m.lists.of(m.ui.focus).Selected()
	if key == "" {
		return nil
	}
	s := m.snap
	switch m.ui.focus {
	case PanelAgents:
		if a := s.agents[key]; a != nil {
			return m.copy(agentDetailPlain(a), "status message")
		}
	case PanelActivity:
		if fl, ok := m.feedLine(key); ok {
			return m.copy(fl.text, "line")
		}
	case PanelForYou:
		for _, it := range m.forYou {
			if it.Key == key {
				return m.copy(CopyText(it.Text), "text")
			}
		}
	case PanelBoard:
		if e, ok := s.board[key]; ok {
			_, _, plain := taskDetail(e, key, m.now)
			return m.copy(plain, "task")
		}
	case PanelPlan: // a task's page text; the done toggle copies nothing
		if task, ok := strings.CutPrefix(key, "task:"); ok {
			_, _, plain := planTaskDetail(s.planState, task, m.now)
			return m.copy(plain, "task")
		}
	case PanelRequests:
		for _, r := range s.requests {
			if "req:"+r.Task == key {
				_, _, plain := requestDetail(r, m.now)
				return m.copy(plain, "request")
			}
		}
		if ths := m.reqThreadsOf(key); len(ths) > 0 {
			var lines []string
			for _, th := range ths {
				text := th.Subject
				if th.Root.ID != "" {
					text = messageBody(th.Root)
				}
				lines = append(lines, text)
			}
			return m.copy(strings.Join(lines, "\n"), "message")
		}
	}
	return nil
}

// movesSelection is a key that moves a list's selection: in Agents it stops
// the selection following the top row; a Tab passing through does not.
func movesSelection(act Action) bool {
	switch act {
	case ActUp, ActDown, ActPageUp, ActPageDown, ActFirst, ActLast:
		return true
	}
	return false
}

// editFilter is the filter line: Enter applies, Esc cancels, Backspace and
// typed characters edit.
func (m *Model) editFilter(act Action, k Key) {
	switch {
	case act == ActSend:
		m.ui.filter = parseFilter(m.ui.edit)
		m.leaveLine(false)
		m.focusPanel(PanelActivity)
	case act == ActEscape:
		m.leaveLine(false)
	default:
		m.editLine(k)
	}
}

// focusPanel moves the focus; a tabbed panel's list becomes its tab, and the
// narrow tier shows the focused view.
func (m *Model) focusPanel(p PanelID) {
	m.ui.focus, m.ui.view = p, p
	if isLowerTab(p) {
		m.ui.tab = p
	}
}

// cyclePanel walks the visible panels (Tab, Shift-Tab); the narrow tier walks
// its views.
func (m *Model) cyclePanel(forward bool) {
	order := m.layout.Visible
	if m.layout.Tier == TierNarrow || len(order) == 0 {
		order = narrowPanels
	}
	i := 0
	for j, p := range order {
		if p == m.ui.focus {
			i = j
		}
	}
	step := 1
	if !forward {
		step = len(order) - 1
	}
	m.focusPanel(order[(i+step)%len(order)])
}

// switchTab is a digit: the tabbed panel's tab, or the narrow tier's view.
func (m *Model) switchTab(n int) {
	tabs := lowerTabs
	if m.layout.Tier == TierNarrow {
		tabs = narrowPanels
	}
	if n < len(tabs) {
		m.focusPanel(tabs[n])
	}
}

// stepTab moves the focus to the lower panel's next or previous tab, wrapping
// as herdr's own next and previous tab do (busmon-keys).
func (m *Model) stepTab(next bool) {
	i := slices.Index(lowerTabs, m.ui.focus)
	if i < 0 {
		return
	}
	step := 1
	if !next {
		step = len(lowerTabs) - 1
	}
	m.focusPanel(lowerTabs[(i+step)%len(lowerTabs)])
}

// askOrWrite is w: the open question as ! opens it, or, with none open, the
// compose window to master (an unsent draft is kept as it is).
func (m *Model) askOrWrite() tea.Cmd {
	if len(openAsks(m.snap.asks)) > 0 {
		agent := ""
		if sel := m.lists.agents.Selected(); m.ui.focus == PanelAgents && m.snap.agents[sel] != nil && m.snap.agents[sel].openAsk > 0 {
			agent = sel
		}
		return m.pushAskOf(agent)
	}
	m.openMessageCompose("", "master")
	return nil
}

// escape clears the focused selection, then Activity's pause, then the
// filter, as busmon's Esc always has. Esc never quits.
func (m *Model) escape() {
	act := &m.lists.activity
	switch l := m.lists.of(m.ui.focus); {
	case m.ui.expanded != "":
		m.collapse()
	case l.Selected() != "":
		l.ClearSelection()
	case !act.Live():
		act.Resume()
	case !m.ui.filter.empty():
		m.ui.filter = feedFilter{}
	}
}

// syncPause keeps Activity's pause cursor with its list, as the list's
// onChange did: leaving live starts counting new lines, live stops it.
func (m *Model) syncPause() {
	switch act := &m.lists.activity; {
	case act.Live():
		m.ui.pause.Resume()
	case !m.ui.pause.Paused():
		m.ui.pause.Pause(m.feed.seq)
	}
}

// nextUnread selects the next unread Activity line after the selection,
// wrapping; false when none.
func (m *Model) nextUnread() bool {
	l := &m.lists.activity
	cur := -1
	for i, fl := range m.feed.feed {
		if fl.id == l.Selected() {
			cur = i
		}
	}
	first := -1
	for i, fl := range m.feed.feed {
		if fl.mark != markUnread {
			continue
		}
		if first < 0 {
			first = i
		}
		if i > cur {
			l.Select(fl.id)
			return true
		}
	}
	if first >= 0 {
		l.Select(m.feed.feed[first].id)
		return true
	}
	return false
}

// wheel scrolls the list under the pointer three rows (listState.ScrollBy); it
// does not move the focus.
func (m *Model) wheel(ms tea.Mouse) {
	if m.ui.tour != nil {
		return
	}
	delta := 3
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -3
	case tea.MouseWheelDown:
	default:
		return
	}
	if m.ui.compose != nil || m.ui.pausePopup != nil || m.ui.reminder != nil || m.ui.summary {
		return // a window or a dialog is over the screen
	}
	if m.rd.open() && m.readerRect().contains(ms.X, ms.Y) {
		m.readerWheel(delta < 0)
		return
	}
	p, ok := m.panelAt(ms.X, ms.Y)
	if !ok {
		return
	}
	if p == PanelAgents {
		m.ui.agentsTouched = true
	}
	m.lists.of(p).ScrollBy(delta)
	m.syncPause()
	m.refresh()
	return
}

// click: a tab label switches the tab, a frame's title row focuses its
// panel, a row is selected (opened when already selected) and the scrollbar
// column pages towards the click (listState.click).
func (m *Model) click(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft {
		return nil
	}
	switch {
	case m.ui.pausePopup != nil: // modal: a click is the popup's
		return m.pauseClick(ms.X, ms.Y)
	case m.ui.tour != nil: // modal too
		cmd := m.tourClick(ms.X, ms.Y)
		m.refresh()
		return cmd
	case m.ui.reminder != nil || m.ui.summary:
		return nil
	case m.ui.compose != nil:
		m.composeClick(ms.X, ms.Y)
		m.refresh()
		return nil
	}
	if m.rd.open() {
		if m.readerRect().contains(ms.X, ms.Y) {
			return m.readerClick(ms.X, ms.Y)
		}
		m.closeReader() // a click on For you, Agents or a tab leaves the reader
	}
	l := m.layout
	if l.Tabs.H > 0 && ms.Y == l.Tabs.Y {
		if p, ok := tabAt(narrowPanels, l.Active, m.width, 0, ms.X); ok {
			m.focusPanel(p)
			m.refresh()
		}
		return nil
	}
	p, ok := m.panelAt(ms.X, ms.Y)
	if !ok {
		return nil
	}
	if p == PanelAgents {
		m.ui.agentsTouched = true
	}
	r := l.rect(p)
	if ms.Y == r.Y {
		if l.Tier != TierNarrow && r == l.Lower {
			if tab, ok := tabAt(lowerTabs, p, r.W-4, r.X+3, ms.X); ok {
				p = tab
			}
		}
		m.focusPanel(p)
		m.refresh()
		return nil
	}
	if m.ui.focus != p {
		m.focusPanel(p)
		m.refresh() // the click lands on the geometry the focus gives
	}
	lst := m.lists.of(p)
	var cmd tea.Cmd
	// A click on the selected row opens it as Enter does (expands, then the
	// reader); one on an expanded row's text opens the reader.
	if i, ok := lst.itemAt(ms.X, ms.Y); ok && (isExpandKey(lst.items[i].Key) || lst.items[i].Key == lst.sel) {
		cmd = m.openTracked(p, false)
	} else {
		lst.click(ms.X, ms.Y)
	}
	m.syncPause()
	m.refresh()
	return cmd
}

// panelAt is the visible panel whose frame holds (x, y).
func (m *Model) panelAt(x, y int) (PanelID, bool) {
	for _, p := range m.layout.Visible {
		r := m.layout.rect(p)
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return p, true
		}
	}
	return 0, false
}

// tabAt is the tab whose label tabBar draws at column x, the bar starting at x0.
func tabAt(tabs []PanelID, active PanelID, width, x0, x int) (PanelID, bool) {
	pos := x0
	for i, label := range tabLabels(tabs, active, width) {
		w := Width(label)
		if x >= pos && x < pos+w {
			return tabs[i], true
		}
		pos += w
	}
	return 0, false
}
