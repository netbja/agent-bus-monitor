package main

import "slices"

// The shell's screen (busmon Charm spec, "The screen"): a two-line header,
// For you and Agents on top, Activity/Board/Work/Plan as one tabbed panel,
// the input line and the footer. Pure geometry, like computeLayout.

// lowerTabs are the tabbed panel's tabs, in key order (1, 2, 3, 4): Plan is
// the fourth (views-redesign §4); Requests is a sub-view, drawn under the tab
// it was opened from (§3).
var lowerTabs = []PanelID{PanelActivity, PanelBoard, PanelWork, PanelPlan}

// narrowPanels are the narrow tier's views, in Tab and digit order; 6 is
// the Plan.
var narrowPanels = []PanelID{PanelForYou, PanelAgents, PanelActivity, PanelBoard, PanelWork, PanelPlan}

// tabbedPanels are the lower panel's lists: its tabs and Work's sub-view.
var tabbedPanels = append(slices.Clone(lowerTabs), PanelRequests)

// shellPanels are every list the shell fills: the views and Work's sub-view.
var shellPanels = append(slices.Clone(narrowPanels), PanelRequests)

// shellCounts is what the shell's layout needs to know about the content.
type shellCounts struct {
	ForYou, Agents int
	ForYouExtra    int  // rows under the items, never cut by the medium cap: the plan pointer (home-inbox-drawer §3.3)
	ForYouWidth    int  // the For you frame's natural width: its widest row, gutter and borders
	Composing      bool // narrow shows the input row only while composing
}

// shellLayout is every zone of one screen size. Lower is the tabbed panel's
// frame (wide, medium) or the one view (narrow); Active is the panel in it.
type shellLayout struct {
	Tier                  Tier
	Header, Tabs          Rect // Tabs: narrow only
	ForYou, Agents, Lower Rect
	Input, Footer         Rect
	ForYouRows, AgentRows int
	Active                PanelID
	Visible               []PanelID // focus order
	// Drawer is the plan drawer's frame at the wide tier, sized to its
	// content when the zone under it keeps room for the active tab (Lower
	// then), zero otherwise (views-redesign §4.2: the free height goes to
	// Activity or the Board).
	Drawer Rect
	// Below is a second panel's frame in the lower zone, under the Plan tab
	// sized to its content (views-redesign §4.2): BelowPanel, Activity,
	// shown and not in the focus order; zero when the Plan fills the zone.
	Below      Rect
	BelowPanel PanelID
}

// minBelow is the least a panel under the Plan tab gets: a frame and four
// rows; less, the Plan keeps the zone.
const minBelow = 6

// fitDrawer sizes the wide drawer to its content, need rows with its
// borders, when the zone under it keeps minShellLower rows for the active
// tab (not the Plan tab itself, which would show the plan twice): Lower
// is then that zone. Otherwise the drawer takes the zone down to the
// footer, as before, and Lower is zero. need 0 (a page opened from the
// drawer): the whole zone.
func fitDrawer(l shellLayout, need int) shellLayout {
	top := l.ForYou.Y + l.ForYou.H
	l.Drawer = Rect{0, top, l.Header.W, l.Footer.Y - top}
	if need > 0 && l.Active != PanelPlan && l.Input.Y-(top+need) >= minShellLower {
		l.Drawer.H = need
		l.Lower = Rect{0, top + need, l.Header.W, l.Input.Y - top - need}
		return l
	}
	l.Lower = Rect{}
	return l
}

// fitPlanTab sizes the Plan tab to its content, need rows with its
// borders, when the zone keeps minBelow rows under it: Activity is drawn
// there (views-redesign §4.2), shown only: focusing it would make it the
// active tab, and the Plan would go.
func fitPlanTab(l shellLayout, need int) shellLayout {
	if l.Active != PanelPlan || l.Tier == TierNarrow || need <= 0 || l.Lower.H-need < minBelow {
		return l
	}
	l.Below = Rect{l.Lower.X, l.Lower.Y + need, l.Lower.W, l.Lower.H - need}
	l.BelowPanel = PanelActivity
	l.Lower.H = need
	return l
}

const (
	minShellLower     = 8 // the tabbed panel at the wide tier
	forYouMediumRows  = 4 // items shown at the medium tier, then "+N more" (the person's decision 1)
	minShellLowerMed  = 6
	shellHeaderHeight = 2
	forYouMinWidth    = 36 // its title and label, "N decide · M merge"
	forYouMaxWidth    = 56 // wide: For you takes its content, capped, Agents the rest (b1 review)
)

// forYouRowsFor is the number of For you rows: its items, at least one (the
// "nothing waits for you" line), at most cap items plus the "+N more" line.
func forYouRowsFor(items, cap int) int {
	if items > cap {
		return cap + 1
	}
	return max(items, 1)
}

// shellLayoutFor decides the shell's geometry. view is the narrow tier's
// panel; tab is the tabbed panel's active tab.
func shellLayoutFor(w, h int, c shellCounts, view, tab PanelID) shellLayout {
	if w < minWidth || h < minHeight {
		return shellLayout{Tier: TierTooSmall}
	}
	if !isLowerTab(tab) {
		tab = PanelActivity
	}
	if w >= wideMinWidth {
		if l, ok := shellWide(w, h, c, tab); ok {
			return l
		}
	}
	if w >= mediumMinWidth {
		if l, ok := shellMedium(w, h, c, tab); ok {
			return l
		}
	}
	return shellNarrow(w, h, c, view)
}

func isLowerTab(p PanelID) bool {
	return p == PanelActivity || p == PanelBoard || p == PanelWork || p == PanelPlan || p == PanelRequests
}

func shellWide(w, h int, c shellCounts, tab PanelID) (shellLayout, bool) {
	const fixed, borders = shellHeaderHeight + 2, 3 // header + input + footer; frame + column header
	avail := h - fixed
	rows := agentRowsFor(c.Agents, avail, borders)
	topH := rows + borders
	if avail-topH < minShellLower {
		return shellLayout{}, false
	}
	fw := clamp(c.ForYouWidth, forYouMinWidth, min(forYouMaxWidth, w*2/5)) // at most 2:3
	y := shellHeaderHeight
	return shellLayout{
		Tier:       TierWide,
		Header:     Rect{0, 0, w, shellHeaderHeight},
		ForYou:     Rect{0, y, fw, topH},
		Agents:     Rect{fw, y, w - fw, topH},
		Lower:      Rect{0, y + topH, w, avail - topH},
		Input:      Rect{0, h - 2, w, 1},
		Footer:     Rect{0, h - 1, w, 1},
		ForYouRows: topH - 2,
		AgentRows:  rows,
		Active:     tab,
		Visible:    []PanelID{PanelForYou, PanelAgents, tab},
	}, true
}

// shellDrawer is the wide layout while the plan drawer is open
// (views-redesign §4.1 point 1): For you takes the whole width and its own
// rows only (its items, at least the "nothing" line, and the plan pointer),
// never more than the top row it had; the drawer takes the rest, under it
// (readerRect). Agents and the lower panel stay where they were, under the
// drawer. The other tiers are returned as they are.
func shellDrawer(l shellLayout, c shellCounts) shellLayout {
	if l.Tier != TierWide {
		return l
	}
	fh := min(max(c.ForYou, 1)+c.ForYouExtra+2, l.ForYou.H)
	l.ForYou = Rect{0, l.ForYou.Y, l.Header.W, fh}
	l.ForYouRows = fh - 2
	return l
}

func shellMedium(w, h int, c shellCounts, tab PanelID) (shellLayout, bool) {
	const fixed, borders = shellHeaderHeight + 2, 2 // header + input + footer; frame only
	avail := h - fixed
	fr := forYouRowsFor(c.ForYou, forYouMediumRows) + c.ForYouExtra
	fh := fr + 2
	rows := agentRowsFor(c.Agents, avail-fh, borders)
	agentsH := rows + borders
	lowerH := avail - fh - agentsH
	if lowerH < minShellLowerMed {
		return shellLayout{}, false
	}
	y := shellHeaderHeight
	return shellLayout{
		Tier:       TierMedium,
		Header:     Rect{0, 0, w, shellHeaderHeight},
		ForYou:     Rect{0, y, w, fh},
		Agents:     Rect{0, y + fh, w, agentsH},
		Lower:      Rect{0, y + fh + agentsH, w, lowerH},
		Input:      Rect{0, h - 2, w, 1},
		Footer:     Rect{0, h - 1, w, 1},
		ForYouRows: fr,
		AgentRows:  rows,
		Active:     tab,
		Visible:    []PanelID{PanelForYou, PanelAgents, tab},
	}, true
}

// shellNarrow keeps today's narrow tier: one view at a time under a tab bar,
// the input only while composing; For you is the first view.
func shellNarrow(w, h int, c shellCounts, view PanelID) shellLayout {
	headerH := 1
	if h >= 12 {
		headerH = shellHeaderHeight
	}
	known := false
	for _, p := range shellPanels { // Requests too: Work's sub-view
		known = known || p == view
	}
	if !known {
		view = PanelForYou
	}
	l := shellLayout{Tier: TierNarrow, Header: Rect{0, 0, w, headerH}, Footer: Rect{0, h - 1, w, 1}, Active: view}
	bottom := h - 1
	if c.Composing {
		l.Input = Rect{0, h - 2, w, 1}
		bottom = h - 2
	}
	top := headerH + 1
	if bottom-top < 3 { // composing on a tiny screen: the tab bar gives way first
		top = headerH
	} else {
		l.Tabs = Rect{0, headerH, w, 1}
	}
	r := Rect{0, top, w, bottom - top}
	switch view {
	case PanelForYou:
		l.ForYou, l.ForYouRows = r, r.H-2
	case PanelAgents:
		l.Agents, l.AgentRows = r, r.H-2
	default:
		l.Lower = r
	}
	l.Visible = []PanelID{view}
	return l
}

// rect is the frame of a panel on this layout (zero when hidden).
func (l shellLayout) rect(p PanelID) Rect {
	switch {
	case p == PanelForYou:
		return l.ForYou
	case p == PanelAgents:
		return l.Agents
	case p == l.Active:
		return l.Lower
	case l.Below.H > 0 && p == l.BelowPanel:
		return l.Below
	}
	return Rect{}
}
