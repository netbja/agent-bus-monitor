package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ListItem is one row of a ListView. Key identifies it across refreshes (the
// selection and the wheel anchor follow keys, never indexes); a non-selectable
// item (a day separator, a section header, a continuation line) is drawn but
// skipped by every movement.
type ListItem struct {
	Key        string
	Line       Line
	Selectable bool
}

// listGutter is the column every row reserves on its left for the selection
// marker, so builders can lay a row out in a fixed width (panel width − 4).
const listGutter = 2

// ListView is busmon's one selectable, scrolling list: the Agents table, the
// Activity feed, the Board and the Requests panel are all instances. It draws
// its own frame (DrawFrame), so the frame, the title, the label and the
// scrollbar have one authority and can never fight over a cell.
//
// The viewport has three modes, decided in this order:
//   - tail: in tail mode and live, the last rows are shown (Activity following
//     the feed); any movement or wheel-up leaves live.
//   - anchored: after a wheel or scrollbar scroll, the item at the top stays at
//     the top across re-sorts, until the next keyboard move.
//   - follow: otherwise the viewport scrolls just enough to keep the selection
//     visible.
type ListView struct {
	*tview.Box
	header       Line
	title, label Line
	items        []ListItem
	sel          string
	selIdx       int // index of sel at the last SetItems/move, for a departed key
	top          int
	anchor       string // keyed item the anchored viewport is pinned to; "" = follow
	anchorOff    int    // rows between that item and the top row
	tail, live   bool
	onChange     func(key string, live bool)
	onOpen       func(key string)
}

// NewListView returns an empty list with no frame title.
func NewListView() *ListView {
	return &ListView{Box: tview.NewBox()}
}

func (l *ListView) SetHeader(h Line) *ListView { l.header = h; return l }

func (l *ListView) SetFrame(title, label Line) { l.title, l.label = title, label }

func (l *ListView) SetOnChange(f func(key string, live bool)) { l.onChange = f }

func (l *ListView) SetOnOpen(f func(key string)) { l.onOpen = f }

func (l *ListView) Selected() string { return l.sel }

func (l *ListView) Live() bool { return l.tail && l.live }

// SetTail turns tail mode on (starting live) or off.
func (l *ListView) SetTail(on bool) {
	l.tail, l.live = on, on
	if on {
		l.sel = ""
	}
}

// SetItems replaces the content. The selection is kept by key; a departed key
// moves the selection to the selectable item now at its former index.
func (l *ListView) SetItems(items []ListItem) {
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
func (l *ListView) Select(key string) {
	i := l.index(key)
	if i < 0 || !l.items[i].Selectable {
		return
	}
	l.setSel(i)
}

// Resume returns a tail-mode list to live: no selection, last rows shown.
func (l *ListView) Resume() {
	if !l.tail || (l.live && l.sel == "") {
		return
	}
	l.sel, l.live, l.anchor = "", true, ""
	l.changed()
}

// Pause leaves live in tail mode without selecting anything: the viewport
// stays where it is (anchored to its top item) while new items arrive below.
func (l *ListView) Pause() {
	if !l.tail || !l.live {
		return
	}
	l.live = false
	l.setAnchor()
	l.changed()
}

// ClearSelection drops the selection without resuming live.
func (l *ListView) ClearSelection() {
	if l.sel == "" {
		return
	}
	l.sel = ""
	l.changed()
}

// Scroll reports the viewport for the scrollbar and the frame label.
func (l *ListView) Scroll() ScrollState {
	return ScrollState{Total: len(l.items), Top: l.top, Visible: l.rows()}
}

// Move moves the selection by delta selectable items (PgUp/PgDn pass ±rows).
// With nothing selected it lands on the newest item in tail mode, else the first.
func (l *ListView) Move(delta int) {
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
	l.setSel(i)
}

// First selects the first selectable item.
func (l *ListView) First() { l.setSel(l.nearestSelectable(0)) }

// Last selects the last item, or in tail mode returns to live.
func (l *ListView) Last() {
	if l.tail {
		l.Resume()
		return
	}
	l.setSel(l.lastSelectable())
}

// Open reports the selected item to the open callback.
func (l *ListView) Open() {
	if l.sel != "" && l.onOpen != nil {
		l.onOpen(l.sel)
	}
}

// ScrollBy moves the viewport only (wheel, scrollbar): it anchors to the item
// at the new top and, in tail mode, leaves live without selecting anything.
func (l *ListView) ScrollBy(delta int) {
	l.top = clamp(l.top+delta, 0, max(0, len(l.items)-l.rows()))
	l.setAnchor()
	if l.tail && l.live && delta < 0 {
		l.live = false
		l.changed()
	}
}

func (l *ListView) setSel(i int) {
	if i < 0 || i >= len(l.items) {
		return
	}
	prevSel, prevLive := l.sel, l.live
	l.sel, l.selIdx, l.anchor = l.items[i].Key, i, ""
	if l.tail {
		l.live = false
	}
	if l.sel != prevSel || l.live != prevLive {
		l.changed()
	}
}

func (l *ListView) setAnchor() {
	l.anchor, l.anchorOff = "", 0
	for i := l.top; i >= 0 && i < len(l.items); i-- {
		if l.items[i].Key != "" {
			l.anchor, l.anchorOff = l.items[i].Key, l.top-i
			return
		}
	}
}

func (l *ListView) changed() {
	if l.onChange != nil {
		l.onChange(l.sel, l.Live())
	}
}

func (l *ListView) index(key string) int {
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
func (l *ListView) nearestSelectable(i int) int {
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

func (l *ListView) lastSelectable() int { return l.nearestSelectable(len(l.items) - 1) }

// showHeader: the column header gives way when the frame has room for only
// one inner row, so a tiny view still shows a data row (spec §8.1).
func (l *ListView) showHeader() bool {
	_, _, _, h := l.GetRect()
	return l.header != nil && h >= 4
}

// inner is the content rectangle: inside the frame, below the header row.
func (l *ListView) inner() (x, y, w, h int) {
	x, y, w, h = l.GetRect()
	x, y, w, h = x+1, y+1, w-2, h-2
	if l.showHeader() {
		y, h = y+1, h-1
	}
	return x, y, max(w, 0), max(h, 0)
}

func (l *ListView) rows() int { _, _, _, h := l.inner(); return h }

// viewport recomputes top for the current mode. Called by Draw only: it reads
// and adjusts list state, never focus, never I/O.
func (l *ListView) viewport() {
	rows, n := l.rows(), len(l.items)
	maxTop := max(0, n-rows)
	switch {
	case l.Live():
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
	l.top = clamp(l.top, 0, maxTop)
}

// Draw draws the frame, the header and the visible rows inside GetRect().
func (l *ListView) Draw(screen tcell.Screen) {
	x, y, w, h := l.GetRect()
	if w < 2 || h < 2 {
		return
	}
	l.viewport()
	DrawFrame(screen, x, y, w, h, Frame{Title: l.title, Label: l.label, Focused: l.HasFocus(), Scroll: l.Scroll()})
	ix, iy, iw, ih := l.inner()
	if iw <= 0 {
		return
	}
	gutter := min(listGutter, iw)
	if l.showHeader() {
		DrawLine(screen, ix, iy-1, gutter, nil, false)
		DrawLine(screen, ix+gutter, iy-1, iw-gutter, l.header, false)
	}
	for r := 0; r < ih; r++ {
		i := l.top + r
		var it ListItem
		if i < len(l.items) {
			it = l.items[i]
		}
		selected := it.Key != "" && it.Key == l.sel
		var mark Line
		if selected {
			mark = Line{{Text: GlyphSelect, Role: RoleAccent}}
		}
		DrawLine(screen, ix, iy+r, gutter, mark, selected)
		DrawLine(screen, ix+gutter, iy+r, iw-gutter, it.Line, selected)
	}
}

// InputHandler handles the movement keys itself so the list works on its own;
// busmon's key table calls the same methods.
func (l *ListView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return l.WrapInputHandler(func(ev *tcell.EventKey, _ func(p tview.Primitive)) {
		switch ev.Key() {
		case tcell.KeyUp:
			l.Move(-1)
		case tcell.KeyDown:
			l.Move(1)
		case tcell.KeyPgUp:
			l.Move(-max(1, l.rows()))
		case tcell.KeyPgDn:
			l.Move(max(1, l.rows()))
		case tcell.KeyHome:
			l.First()
		case tcell.KeyEnd:
			l.Last()
		case tcell.KeyEnter:
			l.Open()
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'k':
				l.Move(-1)
			case 'j':
				l.Move(1)
			case 'g':
				l.First()
			case 'G':
				l.Last()
			case 'v':
				l.Open()
			}
		}
	})
}

// MouseHandler: a click focuses the list and selects the row under it (a
// click on the selected row opens it); the wheel scrolls the viewport only; a
// click on the scrollbar track pages towards the click.
func (l *ListView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return l.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		mx, my := event.Position()
		if !l.InRect(mx, my) {
			return false, nil
		}
		switch action {
		case tview.MouseScrollUp:
			l.ScrollBy(-3)
			return true, nil
		case tview.MouseScrollDown:
			l.ScrollBy(3)
			return true, nil
		case tview.MouseLeftClick:
			setFocus(l)
			x, _, w, _ := l.GetRect()
			_, iy, _, ih := l.inner()
			if my < iy || my >= iy+ih {
				return true, nil
			}
			if mx == x+w-1 && len(l.items) > l.rows() {
				l.pageTowards(my - iy)
				return true, nil
			}
			i := l.top + (my - iy)
			if i >= len(l.items) || !l.items[i].Selectable {
				return true, nil
			}
			if l.items[i].Key == l.sel {
				l.Open()
			} else {
				l.setSel(i)
				l.anchor = "" // the clicked row is visible: follow from here
			}
			return true, nil
		}
		return false, nil
	})
}

// pageTowards scrolls one page up or down depending on whether row (relative
// to the first data row) is above or below the thumb.
func (l *ListView) pageTowards(row int) {
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
