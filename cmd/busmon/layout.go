package main

// Tier is the screen composition busmon picks from the terminal size (spec §3.1).
type Tier int

const (
	TierTooSmall Tier = iota
	TierNarrow
	TierMedium
	TierWide
)

// PanelID names a panel independently of the widget drawing it, so focus and
// selection survive a tier change: Board focused in wide is tab 2 in medium.
type PanelID int

const (
	PanelAgents PanelID = iota
	PanelActivity
	PanelBoard
	PanelRequests
)

// Rect is a screen rectangle in cells. The zero Rect means "not shown".
type Rect struct{ X, Y, W, H int }

// Counts is what the layout needs to know about the content.
type Counts struct {
	Agents, Board int
	Composing     bool // narrow shows the input row only while composing
}

// Layout is every zone's rectangle for one screen size. Frames include their
// borders; AgentRows is the number of agent data rows inside the Agents frame.
type Layout struct {
	Tier                              Tier
	Header                            Rect
	Tabs                              Rect // narrow only
	Agents, Activity, Board, Requests Rect
	Lower                             Rect // medium: the tabbed frame
	Input, Footer                     Rect
	AgentRows                         int
	Visible                           []PanelID // drawn panels, in Tab order
}

const (
	minWidth       = 30
	minHeight      = 6
	wideMinWidth   = 120
	mediumMinWidth = 80
	minAgentRows   = 3
	minActivityH   = 6 // 2 borders + column header + 3 rows
	minBoardH      = 4
	minRequestsH   = 3
	minLowerMedium = 6 // 2 borders + column header + 3 rows
)

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// agentRowsFor applies spec §3.1: min(agentCount, max(3, avail/3 − borders)),
// with an empty roster still owning one row for its "no agent" line.
func agentRowsFor(agents, avail, borders int) int {
	return min(max(agents, 1), max(minAgentRows, avail/3-borders))
}

// computeLayout is the only place that decides geometry. focus is the focused
// panel: narrow's single view, and medium's lower frame unless it is Agents —
// then lower (the last lower tab) fills that frame. Agents is never in the
// lower frame: it falls back to Activity.
func computeLayout(w, h int, c Counts, focus, lower PanelID) Layout {
	active := focus
	if w < minWidth || h < minHeight {
		return Layout{Tier: TierTooSmall}
	}
	if w >= wideMinWidth {
		if l, ok := wideLayout(w, h, c); ok {
			return l
		}
	}
	if w >= mediumMinWidth {
		if focus == PanelAgents {
			active = lower
		}
		if l, ok := mediumLayout(w, h, c, active); ok {
			return l
		}
	}
	return narrowLayout(w, h, c, focus)
}

func wideLayout(w, h int, c Counts) (Layout, bool) {
	const fixed, borders = 3, 3 // header + input + footer; frame + column header
	avail := h - fixed
	rows := agentRowsFor(c.Agents, avail, borders)
	agentsH := rows + borders
	lowerH := avail - agentsH
	if lowerH < max(minActivityH, minBoardH+minRequestsH) {
		return Layout{}, false
	}
	rw := clamp(w*3/10, 40, 56)
	y := 1 + agentsH
	boardH := max(minBoardH, min(max(c.Board, 1)+3, (lowerH+1)/2))
	return Layout{
		Tier:      TierWide,
		Header:    Rect{0, 0, w, 1},
		Agents:    Rect{0, 1, w, agentsH},
		Activity:  Rect{0, y, w - rw, lowerH},
		Board:     Rect{w - rw, y, rw, boardH},
		Requests:  Rect{w - rw, y + boardH, rw, lowerH - boardH},
		Input:     Rect{0, h - 2, w, 1},
		Footer:    Rect{0, h - 1, w, 1},
		AgentRows: rows,
		Visible:   []PanelID{PanelAgents, PanelActivity, PanelBoard, PanelRequests},
	}, true
}

func mediumLayout(w, h int, c Counts, active PanelID) (Layout, bool) {
	const fixed, borders = 4, 2 // 2 header lines + input + footer; frame only
	avail := h - fixed
	rows := agentRowsFor(c.Agents, avail, borders)
	agentsH := rows + borders
	lowerH := avail - agentsH
	if lowerH < minLowerMedium {
		return Layout{}, false
	}
	if active == PanelAgents {
		active = PanelActivity
	}
	l := Layout{
		Tier:      TierMedium,
		Header:    Rect{0, 0, w, 2},
		Agents:    Rect{0, 2, w, agentsH},
		Lower:     Rect{0, 2 + agentsH, w, lowerH},
		Input:     Rect{0, h - 2, w, 1},
		Footer:    Rect{0, h - 1, w, 1},
		AgentRows: rows,
		Visible:   []PanelID{PanelAgents, active},
	}
	l.setPanel(active, l.Lower)
	return l, true
}

func narrowLayout(w, h int, c Counts, active PanelID) Layout {
	headerH := 1
	if h >= 12 {
		headerH = 2
	}
	l := Layout{Tier: TierNarrow, Header: Rect{0, 0, w, headerH}, Footer: Rect{0, h - 1, w, 1}}
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
	view := Rect{0, top, w, bottom - top}
	l.setPanel(active, view)
	l.Visible = []PanelID{active}
	if active == PanelAgents {
		l.AgentRows = view.H - 2
	}
	return l
}

func (l *Layout) setPanel(p PanelID, r Rect) {
	switch p {
	case PanelAgents:
		l.Agents = r
	case PanelActivity:
		l.Activity = r
	case PanelBoard:
		l.Board = r
	case PanelRequests:
		l.Requests = r
	}
}

// panelRect returns the frame of a panel on this layout (zero when hidden).
func (l Layout) panelRect(p PanelID) Rect {
	switch p {
	case PanelAgents:
		return l.Agents
	case PanelActivity:
		return l.Activity
	case PanelBoard:
		return l.Board
	case PanelRequests:
		return l.Requests
	}
	return Rect{}
}
