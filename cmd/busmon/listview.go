package main

// ListItem is one row of a list. Key identifies it across refreshes (the
// selection and the wheel anchor follow keys, never indexes); a non-selectable
// item (a day separator, a section header, a continuation line) is drawn but
// skipped by every movement.
type ListItem struct {
	Key        string
	Line       Line
	Selectable bool
	Expanded   bool // its text is open below it: the gutter shows ▼, not ▶
}

// listGutter is the column every row reserves on its left for the selection
// marker, so builders can lay a row out in a fixed width (panel width − 4).
const listGutter = 2

// listState is busmon's one selectable, scrolling list (For you, Agents,
// Activity, Board, Requests and the picker): keyed selection, tail mode, the
// wheel's anchor, clicks and the scrollbar track. rect and focused are the
// frame's place and focus, set by the owner before drawing or a click.
type listState struct {
	rect         Rect
	focused      bool
	header       Line
	title, label Line
	hue          Role // the frame's colour (panelHue)
	items        []ListItem
	sel          string
	selIdx       int // index of sel at the last SetItems/move, for a departed key
	top          int
	anchor       string // keyed item the anchored viewport is pinned to; "" = follow
	anchorOff    int    // rows between that item and the top row
	tail, live   bool
	keep         int // rows after the selection kept in view with it (the shell's expansion), in every mode
	onChange     func(key string, live bool)
	onOpen       func(key string)
}

func (l *listState) SetHeader(h Line) { l.header = h }

func (l *listState) SetFrame(title, label Line) { l.title, l.label = title, label }

// SetHue gives the list's frame its panel colour.
func (l *listState) SetHue(r Role) { l.hue = r }

func (l *listState) SetOnChange(f func(key string, live bool)) { l.onChange = f }

func (l *listState) SetOnOpen(f func(key string)) { l.onOpen = f }

func (l *listState) Selected() string { return l.sel }

func (l *listState) Live() bool { return l.tail && l.live }

// SetTail turns tail mode on (starting live) or off.
func (l *listState) SetTail(on bool) {
	l.tail, l.live = on, on
	if on {
		l.sel = ""
	}
}

// SetItems replaces the content. The selection is kept by key; a departed key
// moves the selection to the selectable item now at its former index.
func (l *listState) SetItems(items []ListItem) {
	l.items = items
	if l.sel == "" {
		return
	}
	if i := l.index(l.sel); i >= 0 {
		l.selIdx = i
		return
	}
	prev := l.sel
	l.sel = ""
	if len(items) > 0 {
		i := min(l.selIdx, len(items)-1)
		if j := l.nearestSelectable(i); j >= 0 {
			l.sel, l.selIdx = items[j].Key, j
		}
	}
	if l.sel != prev {
		l.changed()
	}
}

// Select selects key (ignored when absent or not selectable) and returns the
// viewport to follow-selection.
func (l *listState) Select(key string) {
	i := l.index(key)
	if i < 0 || !l.items[i].Selectable {
		return
	}
	l.setSel(i)
}

// Resume returns a tail-mode list to live: no selection, last rows shown.
func (l *listState) Resume() {
	if !l.tail || (l.live && l.sel == "") {
		return
	}
	l.sel, l.live, l.anchor = "", true, ""
	l.changed()
}

// Pause leaves live in tail mode without selecting anything: the viewport
// stays where it is (anchored to its top item) while new items arrive below.
func (l *listState) Pause() {
	if !l.tail || !l.live {
		return
	}
	l.live = false
	l.setAnchor()
	l.changed()
}

// ClearSelection drops the selection without resuming live.
func (l *listState) ClearSelection() {
	if l.sel == "" {
		return
	}
	l.sel = ""
	l.changed()
}

// Scroll reports the viewport for the scrollbar and the frame label.
func (l *listState) Scroll() ScrollState {
	return ScrollState{Total: len(l.items), Top: l.top, Visible: l.rows()}
}

// Move moves the selection by delta selectable items (PgUp/PgDn pass ±rows).
// With nothing selected it lands on the newest item in tail mode, else the first.
func (l *listState) Move(delta int) {
	if len(l.items) == 0 {
		return
	}
	cur := l.index(l.sel)
	if l.sel == "" || cur < 0 {
		if l.tail {
			l.setSel(l.lastSelectable())
		} else {
			l.setSel(l.nearestSelectable(0))
		}
		return
	}
	step, i := 1, cur
	if delta < 0 {
		step, delta = -1, -delta
	}
	for n := 0; n < delta; n++ {
		j := i + step
		for j >= 0 && j < len(l.items) && !l.items[j].Selectable {
			j += step
		}
		if j < 0 || j >= len(l.items) {
			break
		}
		i = j
	}
	// A3: moving down onto the newest item of a paused tail list resumes it
	// (the selection stays, as a selection made while live does).
	l.setSel(i)
	if step > 0 && l.tail && !l.live && i == l.lastSelectable() {
		l.live = true
		l.changed()
	}
}

// First selects the first selectable item.
func (l *listState) First() { l.setSel(l.nearestSelectable(0)) }

// Last selects the last item, or in tail mode returns to live.
func (l *listState) Last() {
	if l.tail {
		l.Resume()
		return
	}
	l.setSel(l.lastSelectable())
}

// Open reports the selected item to the open callback.
func (l *listState) Open() {
	if l.sel != "" && l.onOpen != nil {
		l.onOpen(l.sel)
	}
}

// ScrollBy moves the viewport only (wheel, scrollbar): it anchors to the item
// at the new top and, in tail mode, leaves live without selecting anything.
func (l *listState) ScrollBy(delta int) {
	l.top = clamp(l.top+delta, 0, max(0, len(l.items)-l.rows()))
	l.setAnchor()
	switch {
	case l.tail && l.live && delta < 0:
		l.live = false
		l.changed()
	case l.tail && !l.live && delta > 0 && l.top >= max(0, len(l.items)-l.rows()):
		// A3: the wheel back at the bottom resumes auto-scroll.
		l.sel, l.live, l.anchor = "", true, ""
		l.changed()
	}
}

func (l *listState) setSel(i int) {
	if i < 0 || i >= len(l.items) {
		return
	}
	prevSel, prevLive := l.sel, l.live
	l.sel, l.selIdx, l.anchor = l.items[i].Key, i, ""
	if l.sel != prevSel || l.live != prevLive {
		l.changed()
	}
}

func (l *listState) setAnchor() {
	l.anchor, l.anchorOff = "", 0
	for i := l.top; i >= 0 && i < len(l.items); i-- {
		if l.items[i].Key != "" {
			l.anchor, l.anchorOff = l.items[i].Key, l.top-i
			return
		}
	}
}

func (l *listState) changed() {
	if l.onChange != nil {
		l.onChange(l.sel, l.Live())
	}
}

func (l *listState) index(key string) int {
	if key == "" {
		return -1
	}
	for i, it := range l.items {
		if it.Key == key {
			return i
		}
	}
	return -1
}

// nearestSelectable is the first selectable item at or after i, else before it.
func (l *listState) nearestSelectable(i int) int {
	for j := max(i, 0); j < len(l.items); j++ {
		if l.items[j].Selectable {
			return j
		}
	}
	for j := min(i, len(l.items)-1); j >= 0; j-- {
		if l.items[j].Selectable {
			return j
		}
	}
	return -1
}

func (l *listState) lastSelectable() int { return l.nearestSelectable(len(l.items) - 1) }

// showHeader: the column header gives way when the frame has room for only
// one inner row, so a tiny view still shows a data row (spec §8.1).
func (l *listState) showHeader() bool {
	return l.header != nil && l.rect.H >= 4
}

// inner is the content rectangle: inside the frame, below the header row.
func (l *listState) inner() (x, y, w, h int) {
	x, y, w, h = l.rect.X, l.rect.Y, l.rect.W, l.rect.H
	x, y, w, h = x+1, y+1, w-2, h-2
	if l.showHeader() {
		y, h = y+1, h-1
	}
	return x, y, max(w, 0), max(h, 0)
}

func (l *listState) rows() int { _, _, _, h := l.inner(); return h }

// viewport recomputes top for the current mode. Called by Draw only: it reads
// and adjusts list state, never focus, never I/O.
func (l *listState) viewport() {
	rows, n := l.rows(), len(l.items)
	maxTop := max(0, n-rows)
	// A wheel over a kept expansion anchors the list, in auto-scroll too:
	// the wheel wins until the next key.
	wheeled := l.keep > 0 && l.anchor != ""
	switch {
	case l.Live() && !wheeled:
		l.top = maxTop
	case l.anchor != "":
		if i := l.index(l.anchor); i >= 0 {
			l.top = i + l.anchorOff
		}
	default:
		if i := l.index(l.sel); i >= 0 && rows > 0 {
			if i < l.top {
				l.top = i
			} else if i >= l.top+rows {
				l.top = i - rows + 1
			}
		}
	}
	// An expanded selection and its text stay in view, the row first when
	// both do not fit: the tail does not push them off the top. A wheel
	// scrolls away from them.
	if i := l.index(l.sel); l.keep > 0 && !wheeled && i >= 0 && rows > 0 {
		if end := i + l.keep; end >= l.top+rows {
			l.top = min(i, end-rows+1)
		}
		l.top = min(l.top, i)
	}
	l.top = clamp(l.top, 0, maxTop)
}

// draw draws the frame, the header and the visible rows inside rect.
func (l *listState) draw(screen cellSink) {
	x, y, w, h := l.rect.X, l.rect.Y, l.rect.W, l.rect.H
	if w < 2 || h < 2 {
		return
	}
	l.viewport()
	drawFrame(screen, x, y, w, h, Frame{Title: l.title, Label: l.label, Focused: l.focused, Scroll: l.Scroll(), Hue: l.hue})
	ix, iy, iw, ih := l.inner()
	if iw <= 0 {
		return
	}
	gutter := min(listGutter, iw)
	if l.showHeader() {
		drawLine(screen, ix, iy-1, gutter, nil, false)
		drawLine(screen, ix+gutter, iy-1, iw-gutter, l.header, false)
	}
	for r := 0; r < ih; r++ {
		i := l.top + r
		var it ListItem
		if i < len(l.items) {
			it = l.items[i]
		}
		selected := it.Key != "" && it.Key == l.sel
		var mark Line
		switch {
		case it.Expanded:
			mark = Line{{Text: GlyphDown, Role: RoleAccent}}
		case selected:
			mark = Line{{Text: GlyphSelect, Role: RoleAccent}}
		}
		drawLine(screen, ix, iy+r, gutter, mark, selected)
		drawLine(screen, ix+gutter, iy+r, iw-gutter, it.Line, selected)
	}
}

// click selects the row under (mx, my) or opens it when it is the selected
// one; on the scrollbar column it pages towards the click.
func (l *listState) click(mx, my int) {
	x, w := l.rect.X, l.rect.W
	_, iy, _, ih := l.inner()
	if my < iy || my >= iy+ih {
		return
	}
	if mx == x+w-1 && len(l.items) > l.rows() {
		l.pageTowards(my - iy)
		return
	}
	i := l.top + (my - iy)
	if i >= len(l.items) || !l.items[i].Selectable {
		return
	}
	if l.items[i].Key == l.sel {
		l.Open()
	} else {
		l.setSel(i)
		l.anchor = "" // the clicked row is visible: follow from here
	}
}

// itemAt is the index of the row under (mx, my), off the scrollbar column.
func (l *listState) itemAt(mx, my int) (int, bool) {
	_, iy, _, ih := l.inner()
	if my < iy || my >= iy+ih || mx <= l.rect.X || mx >= l.rect.X+l.rect.W-1 {
		return 0, false
	}
	i := l.top + (my - iy)
	return i, i < len(l.items)
}

// pageTowards scrolls one page up or down depending on whether row (relative
// to the first data row) is above or below the thumb.
func (l *listState) pageTowards(row int) {
	rows := l.rows()
	track := rows - 2
	if track <= 0 {
		return
	}
	n := len(l.items)
	thumb := max(1, min(track, track*rows/n))
	maxTop := max(1, n-rows)
	start := 1 + (track-thumb)*min(l.top, maxTop)/maxTop
	switch {
	case row < start:
		l.ScrollBy(-rows)
	case row >= start+thumb:
		l.ScrollBy(rows)
	}
}
