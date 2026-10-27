package main

import (
	"fmt"
	"strings"
	"testing"
)

// The one list's rules (listState: keyed selection, tail and pause, the
// wheel's anchor, clicks and the scrollbar track), drawn on the shell's grid.

// recSink is a grid that records every write outside rect.
type recSink struct {
	*grid
	rect Rect
	bad  []string
}

func (r *recSink) Put(x, y int, cluster string, st cellStyle) {
	if x < r.rect.X || y < r.rect.Y || x >= r.rect.X+r.rect.W || y >= r.rect.Y+r.rect.H {
		r.bad = append(r.bad, fmt.Sprintf("(%d,%d)%q", x, y, cluster))
	}
	r.grid.Put(x, y, cluster, st)
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
func newList(w, h int, its []ListItem) *listState {
	l := &listState{rect: Rect{0, 0, w, h}}
	l.SetItems(its)
	return l
}

// drawList draws once so the viewport is computed, and returns the grid; a
// write outside the list's rect fails the test.
func drawList(t *testing.T, l *listState) *grid {
	t.Helper()
	r := l.rect
	s := &recSink{grid: newGrid(r.X+r.W+2, r.Y+r.H+2), rect: r}
	l.draw(s)
	if len(s.bad) > 0 {
		t.Fatalf("writes outside the list rect: %v", s.bad)
	}
	return s.grid
}

// rowText is the cells [x0, x1) of row y.
func rowText(g *grid, y, x0, x1 int) string {
	var b strings.Builder
	for x := x0; x < x1; x++ {
		b.WriteString(g.at(x, y).text)
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
		l.Move(1)
	}
	drawList(t, l)
	if l.Selected() != "k05" || l.Scroll().Top != 2 {
		t.Fatalf("after 5 downs: sel %q top %d, want k05 top 2", l.Selected(), l.Scroll().Top)
	}
	l.Move(-1)
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
		l.ScrollBy(3)
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
	l.Move(-1)
	if !l.Live() || l.Selected() != "k11" {
		t.Fatalf("Up from live: live %v sel %q, want still live with newest k11 selected", l.Live(), l.Selected())
	}
	l.Pause()
	l.SetItems(numbered(15))
	drawList(t, l)
	if l.Scroll().Top != 8 {
		t.Fatalf("paused view moved: top %d", l.Scroll().Top)
	}
	l.Last()
	drawList(t, l)
	if !l.Live() || l.Selected() != "" || l.Scroll().Top != 11 {
		t.Fatalf("End: live %v sel %q top %d", l.Live(), l.Selected(), l.Scroll().Top)
	}
	l.ScrollBy(-3)
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
	l.click(5, 2) // second data row
	if l.Selected() != "b" || len(opened) != 0 {
		t.Fatalf("first click: sel %q opened %v", l.Selected(), opened)
	}
	l.click(5, 2)
	if fmt.Sprint(opened) != "[b]" {
		t.Fatalf("second click opened %v", opened)
	}
	l.Open() // Enter
	l.Open() // v
	if fmt.Sprint(opened) != "[b b b]" {
		t.Fatalf("Enter/v opened %v", opened)
	}
	if l.click(5, 6); l.Selected() != "b" {
		t.Fatalf("click below the items changed the selection to %q", l.Selected())
	}
}

func TestTrackClickPages(t *testing.T) {
	l := newList(30, 10, numbered(50)) // 8 rows; scrollbar at x=29
	l.Select("k00")
	drawList(t, l)
	l.click(29, 7) // track, below the thumb
	drawList(t, l)
	if l.Scroll().Top != 8 {
		t.Fatalf("track click below thumb: top %d, want 8", l.Scroll().Top)
	}
	l.click(29, 1) // the ▲ cell, above the thumb
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
	l.Move(1)
	if l.Selected() != "a" {
		t.Fatalf("first Down selected %q, want first selectable a", l.Selected())
	}
	l.Move(1)
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
		l := &listState{rect: Rect{4, 2, sz[0], sz[1]}}
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
	if str := s.at(19, 2).text; str != "│" {
		t.Fatalf("right border %q", str)
	}
}

func TestHeaderYieldsInTinyFrame(t *testing.T) {
	its := []ListItem{{Key: "a", Line: Line{{Text: "row-a"}}, Selectable: true}}
	for _, h := range []int{3, 4} {
		l := newList(20, h, its)
		l.SetHeader(Line{{Text: "HEAD"}})
		got := drawList(t, l).plain()
		if !strings.Contains(got, "row-a") {
			t.Errorf("h=%d: no data row drawn: %q", h, got)
		}
		if wantHead := h >= 4; strings.Contains(got, "HEAD") != wantHead {
			t.Errorf("h=%d: header shown=%v, want %v", h, !wantHead, wantHead)
		}
	}
}
