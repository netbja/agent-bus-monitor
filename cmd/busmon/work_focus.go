package main

// Work's Kanban focus (busmon-work-focus, the person's brief of 2026-11-13):
// two levels under the tabs, so the Kanban is never a trap.
//
//   - the tab (workAtTabs): ←→ step the tabs as on Activity and Board; ↓, j
//     or Enter go down to the column selector. 1-4 change tab anywhere.
//   - the column selector (workAtCols): ←→ choose a column, its heading
//     drawn selected; ↓, j or Enter go into it; ↑, k or Esc back to the tab.
//   - a column's cards (workAtCards): ↑↓ j k walk that column only, never
//     past its first or last card into the next; ←→ go to the next column's
//     card at the same rank; Enter opens the card; Esc back to the selector.
//
// Inside the columns (the selector and the cards) Tab and shift-Tab are →
// and ← (the person's follow-up, busmon-work-tab-header); at the tab they
// keep moving the focus to the next panel, the way out of Work. The column
// holding the cursor has its heading in the Work tab's colour.
//
// The tab bar's pill is full only at the tab's level, faded once the focus
// is inside the columns: where the focus is shows on the screen.
type workLevel int

const (
	workAtTabs workLevel = iota
	workAtCols
	workAtCards
)

// workKanbanKey is a key on Work's Kanban at its focus level; false leaves
// the key to the shell (the digits, y, + -, d, ...).
func (m *Model) workKanbanKey(act Action) bool {
	l := &m.lists.work
	k := l.kanban
	n := len(k.cols)
	if n == 0 {
		return false
	}
	m.ui.workCol = min(max(m.ui.workCol, 0), n-1)
	if m.ui.workLevel != workAtTabs {
		switch act {
		case ActNextPanel:
			act = ActTabNext
		case ActPrevPanel:
			act = ActTabPrev
		}
	}
	switch m.ui.workLevel {
	case workAtTabs:
		switch act {
		case ActTabNext, ActTabPrev:
			m.stepTab(act == ActTabNext)
		case ActDown, ActOpen:
			if c, _ := k.cardPlace(l.Selected()); c >= 0 {
				m.ui.workCol = c
			}
			m.ui.workLevel = workAtCols
		case ActUp, ActPageUp, ActPageDown, ActFirst, ActLast:
		default:
			return false
		}
	case workAtCols:
		switch act {
		case ActTabNext:
			m.ui.workCol = (m.ui.workCol + 1) % n
		case ActTabPrev:
			m.ui.workCol = (m.ui.workCol + n - 1) % n
		case ActDown, ActOpen:
			key := l.Selected()
			if c, _ := k.cardPlace(key); c != m.ui.workCol {
				key = k.cardOf(m.ui.workCol, 0)
			}
			if key == "" {
				m.ui.flash = "nothing in " + k.colName(m.ui.workCol)
				break
			}
			l.Select(key)
			m.ui.workLevel = workAtCards
		case ActUp, ActEscape:
			m.ui.workLevel = workAtTabs
		case ActPageUp, ActPageDown, ActFirst, ActLast:
		default:
			return false
		}
	case workAtCards:
		sel := l.Selected()
		c, r := k.cardPlace(sel)
		if c < 0 { // the card went: back to the selector
			m.ui.workLevel = workAtCols
			return act == ActUp || act == ActDown || act == ActTabNext || act == ActTabPrev || act == ActEscape
		}
		m.ui.workCol = c // the card's column, however the cards were reached
		last := k.cardCount(c) - 1
		to := r
		switch act {
		case ActTabNext, ActTabPrev:
			if key := k.step(sel, act == ActTabNext); key != "" {
				l.Select(key)
				m.ui.workCol, _ = k.cardPlace(key)
			}
			return true
		case ActUp:
			to = r - 1
		case ActDown:
			to = r + 1
		case ActPageUp, ActFirst:
			to = 0
		case ActPageDown, ActLast:
			to = last
		case ActEscape:
			m.ui.workCol, m.ui.workLevel = c, workAtCols
			return true
		default:
			return false
		}
		l.Select(k.cardOf(c, min(max(to, 0), last))) // never past the column's ends
	}
	return true
}

// cardPlace is a card's column and its rank among the column's selectable
// cards; -1, -1 when no card has that key.
func (k *kanbanView) cardPlace(key string) (col, rank int) {
	if key == "" {
		return -1, -1
	}
	for c, kc := range k.cols {
		r := 0
		for _, card := range kc.cards {
			if card.key == "" {
				continue
			}
			if card.key == key {
				return c, r
			}
			r++
		}
	}
	return -1, -1
}

// cardOf is the key of column c's selectable card at rank r, "" for none.
func (k *kanbanView) cardOf(c, r int) string {
	if c < 0 || c >= len(k.cols) {
		return ""
	}
	for _, card := range k.cols[c].cards {
		if card.key == "" {
			continue
		}
		if r == 0 {
			return card.key
		}
		r--
	}
	return ""
}

// cardCount is how many selectable cards column c holds.
func (k *kanbanView) cardCount(c int) int {
	n := 0
	for _, card := range k.cols[c].cards {
		if card.key != "" {
			n++
		}
	}
	return n
}

// colName is column c's heading without its count ("For you").
func (k *kanbanView) colName(c int) string {
	t := k.cols[c].title
	for i := len(t) - 1; i > 0; i-- {
		if t[i] == ' ' && i > 0 && t[i-1] == ' ' {
			return t[:i-1]
		}
	}
	return t
}
