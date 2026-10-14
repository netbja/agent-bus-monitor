package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// recScreen records every write outside the rectangle the drawing primitive
// owns. A plain SimulationScreen would clip such writes silently.
type recScreen struct {
	tcell.SimulationScreen
	rect Rect
	bad  []string
}

func (r *recScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if x < r.rect.X || y < r.rect.Y || x >= r.rect.X+r.rect.W || y >= r.rect.Y+r.rect.H {
		r.bad = append(r.bad, fmt.Sprintf("(%d,%d)%q", x, y, primary))
	}
	r.SimulationScreen.SetContent(x, y, primary, combining, style)
}

func (r *recScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	if x < r.rect.X || y < r.rect.Y || x >= r.rect.X+r.rect.W || y >= r.rect.Y+r.rect.H {
		r.bad = append(r.bad, fmt.Sprintf("(%d,%d)%q", x, y, str))
	}
	return r.SimulationScreen.Put(x, y, str, style)
}

func (r *recScreen) PutStr(x, y int, str string) { r.PutStrStyled(x, y, str, tcell.StyleDefault) }

func (r *recScreen) PutStrStyled(x, y int, str string, style tcell.Style) {
	r.bad = append(r.bad, fmt.Sprintf("PutStr(%d,%d) bypasses cell accounting", x, y))
	r.SimulationScreen.PutStrStyled(x, y, str, style)
}

func newRecScreen(t *testing.T, w, h int, rect Rect) *recScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return &recScreen{SimulationScreen: s, rect: rect}
}

func items(keys ...string) []ListItem {
	out := make([]ListItem, len(keys))
	for i, k := range keys {
		out[i] = ListItem{Key: k, Line: Line{{Text: "item " + k, Role: RoleText}}, Selectable: true}
	}
	return out
}

func numbered(n int) []ListItem {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}
	return items(keys...)
}

// newList places a list at (0,0) w×h, inner rows = h-2.
func newList(w, h int, its []ListItem) *ListView {
	l := NewListView()
	l.SetRect(0, 0, w, h)
	l.SetItems(its)
	return l
}

func key(k tcell.Key) *tcell.EventKey       { return tcell.NewEventKey(k, 0, tcell.ModNone) }
func runeKey(r rune) *tcell.EventKey        { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }
func press(l *ListView, ev *tcell.EventKey) { l.InputHandler()(ev, func(tview.Primitive) {}) }

func mouse(l *ListView, a tview.MouseAction, x, y int) bool {
	btn := tcell.ButtonPrimary
	switch a {
	case tview.MouseScrollUp:
		btn = tcell.WheelUp
	case tview.MouseScrollDown:
		btn = tcell.WheelDown
	}
	consumed, _ := l.MouseHandler()(a, tcell.NewEventMouse(x, y, btn, tcell.ModNone), func(tview.Primitive) {})
	return consumed
}

// draw renders once so the viewport is computed, and returns the screen.
func drawList(t *testing.T, l *ListView) *recScreen {
	t.Helper()
	x, y, w, h := l.GetRect()
	s := newRecScreen(t, x+w+2, y+h+2, Rect{x, y, w, h})
	l.Draw(s)
	if len(s.bad) > 0 {
		t.Fatalf("writes outside the list rect: %v", s.bad)
	}
	return s
}

func rowText(s tcell.Screen, y, x0, x1 int) string {
	var b strings.Builder
	for x := x0; x < x1; x++ {
		str, _, _ := s.Get(x, y)
		b.WriteString(str)
	}
	return b.String()
}

func TestSelectionByKeySurvivesReorder(t *testing.T) {
	l := newList(30, 8, items("a", "b", "c", "d"))
	l.Select("c")
	l.SetItems(items("d", "c", "b", "a"))
	if l.Selected() != "c" {
		t.Fatalf("selection %q after reorder, want c", l.Selected())
	}
}

func TestDepartedSelection(t *testing.T) {
	l := newList(30, 8, items("a", "b", "c", "d"))
	l.Select("c") // index 2
	l.SetItems(items("a", "b", "d"))
	if l.Selected() != "d" {
		t.Fatalf("departed c → %q, want d (item now at index 2)", l.Selected())
	}
	l.Select("d")
	l.SetItems(items("a"))
	if l.Selected() != "a" {
		t.Fatalf("clamped selection %q, want a", l.Selected())
	}
	l.SetItems(nil)
	if l.Selected() != "" {
		t.Fatalf("empty list keeps selection %q", l.Selected())
	}
}

func TestFollowSelectionScrollsMinimal(t *testing.T) {
	l := newList(30, 6, numbered(20)) // 4 rows
	l.Select("k00")
	drawList(t, l)
	for i := 0; i < 5; i++ {
		press(l, key(tcell.KeyDown))
	}
	drawList(t, l)
	if l.Selected() != "k05" || l.Scroll().Top != 2 {
		t.Fatalf("after 5 downs: sel %q top %d, want k05 top 2", l.Selected(), l.Scroll().Top)
	}
	press(l, key(tcell.KeyUp))
	drawList(t, l)
	if l.Scroll().Top != 2 {
		t.Fatalf("moving up inside the viewport scrolled: top %d", l.Scroll().Top)
	}
}

func TestWheelAnchorSurvivesReorder(t *testing.T) {
	its := numbered(50)
	l := newList(30, 10, its) // 8 rows
	l.Select("k00")
	drawList(t, l)
	for i := 0; i < 3; i++ {
		mouse(l, tview.MouseScrollDown, 5, 4)
	}
	drawList(t, l)
	top := l.Scroll().Top
	if top == 0 {
		t.Fatal("wheel did not scroll")
	}
	topKey := its[top].Key
	// a tick reorders: move the last ten items to the front
	re := append(append([]ListItem{}, its[40:]...), its[:40]...)
	l.SetItems(re)
	s := drawList(t, l)
	if got := rowText(s, 1, 3, 3+len("item "+topKey)); got != "item "+topKey {
		t.Fatalf("top row after reorder %q, want item %s", got, topKey)
	}
	if l.Selected() != "k00" {
		t.Fatalf("selection moved to %q", l.Selected())
	}
}

func TestTailModeAndPause(t *testing.T) {
	l := newList(30, 6, numbered(10)) // 4 rows
	l.SetTail(true)
	var changes []string
	l.SetOnChange(func(k string, live bool) { changes = append(changes, fmt.Sprintf("%s/%v", k, live)) })
	drawList(t, l)
	if !l.Live() || l.Selected() != "" || l.Scroll().Top != 6 {
		t.Fatalf("tail start: live %v sel %q top %d", l.Live(), l.Selected(), l.Scroll().Top)
	}
	l.SetItems(numbered(12))
	drawList(t, l)
	if l.Scroll().Top != 8 {
		t.Fatalf("live tail did not follow: top %d", l.Scroll().Top)
	}
	press(l, key(tcell.KeyUp))
	if !l.Live() || l.Selected() != "k11" {
		t.Fatalf("Up from live: live %v sel %q, want still live with newest k11 selected", l.Live(), l.Selected())
	}
	l.Pause()
	l.SetItems(numbered(15))
	drawList(t, l)
	if l.Scroll().Top != 8 {
		t.Fatalf("paused view moved: top %d", l.Scroll().Top)
	}
	press(l, key(tcell.KeyEnd))
	drawList(t, l)
	if !l.Live() || l.Selected() != "" || l.Scroll().Top != 11 {
		t.Fatalf("End: live %v sel %q top %d", l.Live(), l.Selected(), l.Scroll().Top)
	}
	mouse(l, tview.MouseScrollUp, 5, 3)
	if l.Live() || l.Selected() != "" {
		t.Fatalf("wheel up must pause without selecting: live %v sel %q", l.Live(), l.Selected())
	}
	l.Resume()
	if !l.Live() {
		t.Fatal("Resume did not return to live")
	}
	want := []string{"k11/true", "k11/false", "/true", "/false", "/true"}
	if fmt.Sprint(changes) != fmt.Sprint(want) {
		t.Fatalf("OnChange %v, want %v", changes, want)
	}
}

func TestClickSelectsThenOpens(t *testing.T) {
	l := newList(30, 8, items("a", "b", "c"))
	var opened []string
	l.SetOnOpen(func(k string) { opened = append(opened, k) })
	drawList(t, l)
	mouse(l, tview.MouseLeftClick, 5, 2) // second data row
	if l.Selected() != "b" || len(opened) != 0 {
		t.Fatalf("first click: sel %q opened %v", l.Selected(), opened)
	}
	mouse(l, tview.MouseLeftClick, 5, 2)
	if fmt.Sprint(opened) != "[b]" {
		t.Fatalf("second click opened %v", opened)
	}
	press(l, key(tcell.KeyEnter))
	press(l, runeKey('v'))
	if fmt.Sprint(opened) != "[b b b]" {
		t.Fatalf("Enter/v opened %v", opened)
	}
	if mouse(l, tview.MouseLeftClick, 5, 6) && l.Selected() != "b" {
		t.Fatalf("click below the items changed the selection to %q", l.Selected())
	}
}

func TestTrackClickPages(t *testing.T) {
	l := newList(30, 10, numbered(50)) // 8 rows; scrollbar at x=29
	l.Select("k00")
	drawList(t, l)
	mouse(l, tview.MouseLeftClick, 29, 7) // track, below the thumb
	drawList(t, l)
	if l.Scroll().Top != 8 {
		t.Fatalf("track click below thumb: top %d, want 8", l.Scroll().Top)
	}
	mouse(l, tview.MouseLeftClick, 29, 1) // the ▲ cell, above the thumb
	drawList(t, l)
	if l.Scroll().Top != 0 {
		t.Fatalf("track click above thumb: top %d, want 0", l.Scroll().Top)
	}
}

func TestNonSelectableSkipped(t *testing.T) {
	its := []ListItem{
		{Key: "s1", Line: Line{{Text: "── Mon ──"}}},
		{Key: "a", Line: Line{{Text: "a"}}, Selectable: true},
		{Key: "s2", Line: Line{{Text: "── Tue ──"}}},
		{Key: "b", Line: Line{{Text: "b"}}, Selectable: true},
	}
	l := newList(30, 8, its)
	press(l, key(tcell.KeyDown))
	if l.Selected() != "a" {
		t.Fatalf("first Down selected %q, want first selectable a", l.Selected())
	}
	press(l, key(tcell.KeyDown))
	if l.Selected() != "b" {
		t.Fatalf("Down skipped to %q, want b", l.Selected())
	}
	l.Select("s1")
	if l.Selected() != "b" {
		t.Fatalf("Select on a separator changed selection to %q", l.Selected())
	}
}

func TestDrawStaysInRect(t *testing.T) {
	long := Line{{Text: strings.Repeat("日本語👩‍💻", 30), Role: RoleText}}
	for _, sz := range [][2]int{{30, 6}, {12, 4}, {3, 3}, {2, 2}, {80, 3}} {
		its := numbered(40)
		its[3].Line = long
		l := NewListView()
		l.SetRect(4, 2, sz[0], sz[1])
		l.SetHeader(Line{{Text: "agent   state", Role: RoleDim}})
		l.SetFrame(Line{{Text: "Agents"}}, Line{{Text: "1-8 of 40", Role: RoleMuted}})
		l.SetItems(its)
		l.Select("k03")
		drawList(t, l) // fails on any out-of-rect write
	}
}

func TestDrawRowsAndGutter(t *testing.T) {
	l := newList(20, 6, items("a", "b"))
	l.SetHeader(Line{{Text: "name", Role: RoleDim}})
	l.Select("b")
	s := drawList(t, l)
	if got := rowText(s, 1, 1, 9); got != "  name  " {
		t.Fatalf("header row %q", got)
	}
	if got := rowText(s, 2, 1, 9); got != "  item a" {
		t.Fatalf("unselected row %q", got)
	}
	if got := rowText(s, 3, 1, 9); got != GlyphSelect+" item b" {
		t.Fatalf("selected row %q", got)
	}
	if str, _, _ := s.Get(19, 2); str != "│" {
		t.Fatalf("right border %q", str)
	}
}

func TestHeaderYieldsInTinyFrame(t *testing.T) {
	its := []ListItem{{Key: "a", Line: Line{{Text: "row-a"}}, Selectable: true}}
	for _, h := range []int{3, 4} {
		l := newList(20, h, its)
		l.SetHeader(Line{{Text: "HEAD"}})
		s := newRecScreen(t, 20, h, Rect{0, 0, 20, h})
		l.Draw(s)
		s.Show()
		cells, _, _ := s.GetContents()
		var sb strings.Builder
		for _, c := range cells {
			sb.WriteString(string(c.Runes))
		}
		got := sb.String()
		if !strings.Contains(got, "row-a") {
			t.Errorf("h=%d: no data row drawn: %q", h, got)
		}
		if wantHead := h >= 4; strings.Contains(got, "HEAD") != wantHead {
			t.Errorf("h=%d: header shown=%v, want %v", h, !wantHead, wantHead)
		}
	}
}
