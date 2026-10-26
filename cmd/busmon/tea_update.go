package main

import (
	tea "charm.land/bubbletea/v2"
)

// Navigation and the lists' mouse (busmon Charm spec, step b2): keys go
// through keys.go's tables (ModeShell, ModeFilter), the lists keep
// listState's rules, and every change ends in refresh.

// mode is who owns the keyboard in the shell.
func (m *Model) mode() Mode {
	if m.ui.editing {
		return ModeFilter
	}
	return ModeShell
}

// key performs a key press.
func (m Model) key(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := keyOfTea(msg)
	mode := m.mode()
	act := lookup(mode, m.ui.focus, m.layout.Tier, k)
	if act == ActQuit {
		return m, tea.Quit
	}
	if act != ActNone {
		m.ui.flash, m.ui.flashErr = "", false
	}
	if mode == ModeFilter {
		m.editFilter(act, k)
		m.refresh()
		return m, nil
	}
	l := m.lists.of(m.ui.focus)
	if m.ui.focus == PanelAgents && act != ActNone {
		m.ui.agentsTouched = true
	}
	switch act {
	case ActNextPanel, ActPrevPanel:
		m.cyclePanel(act == ActNextPanel)
	case ActTab1, ActTab2, ActTab3, ActTab4:
		m.switchTab(int(act - ActTab1))
	case ActTab5:
		m.switchTab(4)
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
	case ActOpen:
		l.Open() // the inline expansion and the reader come with step b3
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
		m.ui.editing, m.ui.edit = true, m.ui.filter.raw
		m.focusPanel(PanelActivity)
	case ActEscape:
		m.escape()
	}
	m.syncPause()
	m.refresh()
	return m, nil
}

// editFilter is the filter line: Enter applies, Esc cancels, Backspace and
// typed characters edit.
func (m *Model) editFilter(act Action, k Key) {
	switch {
	case act == ActSend:
		m.ui.filter = parseFilter(m.ui.edit)
		m.ui.editing = false
		m.focusPanel(PanelActivity)
	case act == ActEscape:
		m.ui.editing = false
	case k.Code == keyBackspace:
		if r := []rune(m.ui.edit); len(r) > 0 {
			m.ui.edit = string(r[:len(r)-1])
		}
	case k.Code == keyRune && !k.Alt:
		m.ui.edit += string(k.Rune)
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

// escape clears the focused selection, then Activity's pause, then the
// filter, as tview's escape does. Esc never quits.
func (m *Model) escape() {
	act := &m.lists.activity
	switch l := m.lists.of(m.ui.focus); {
	case l.Selected() != "":
		l.ClearSelection()
	case !act.Live():
		act.Resume()
	case !m.ui.filter.empty():
		m.ui.filter = feedFilter{}
	}
}

// syncPause keeps Activity's pause cursor with its list, as tview's
// onChange does: leaving live starts counting new lines, live stops it.
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

// wheel scrolls the list under the pointer three rows, as ListView does; it
// does not move the focus.
func (m Model) wheel(ms tea.Mouse) (Model, tea.Cmd) {
	p, ok := m.panelAt(ms.X, ms.Y)
	if !ok {
		return m, nil
	}
	delta := 3
	switch ms.Button {
	case tea.MouseWheelUp:
		delta = -3
	case tea.MouseWheelDown:
	default:
		return m, nil
	}
	if p == PanelAgents {
		m.ui.agentsTouched = true
	}
	m.lists.of(p).ScrollBy(delta)
	m.syncPause()
	m.refresh()
	return m, nil
}

// click: a tab label switches the tab, a frame's title row focuses its
// panel, a row is selected (opened when already selected) and the scrollbar
// column pages towards the click, as ListView does.
func (m Model) click(ms tea.Mouse) (Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft {
		return m, nil
	}
	l := m.layout
	if l.Tabs.H > 0 && ms.Y == l.Tabs.Y {
		if p, ok := tabAt(narrowPanels, l.Active, m.width, 0, ms.X); ok {
			m.focusPanel(p)
			m.refresh()
		}
		return m, nil
	}
	p, ok := m.panelAt(ms.X, ms.Y)
	if !ok {
		return m, nil
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
		return m, nil
	}
	if m.ui.focus != p {
		m.focusPanel(p)
		m.refresh() // the click lands on the geometry the focus gives
	}
	m.lists.of(p).click(ms.X, ms.Y)
	m.syncPause()
	m.refresh()
	return m, nil
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
	for i, s := range tabBar(tabs, active, width) {
		w := Width(s.Text)
		if x >= pos && x < pos+w && i < len(tabs) {
			return tabs[i], true
		}
		pos += w
	}
	return 0, false
}
