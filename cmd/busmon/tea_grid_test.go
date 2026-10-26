package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/gdamore/tcell/v2"

	"charm.land/lipgloss/v2"
)

// placed is a View's content as bubbletea's renderer lays it on a terminal
// with the given width method: the text of each row, cell by cell.
func placed(content string, w, h int, m ansi.Method) string {
	buf := uv.NewScreenBuffer(w, h)
	buf.Method = m
	uv.NewStyledString(content).Draw(buf, buf.Bounds())
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			c := buf.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue // the continuation of a wide cell
			}
			sb.WriteString(c.Content)
		}
		rows[y] = sb.String()
	}
	return strings.Join(rows, "\n")
}

func TestGridPutKeepsWideClustersWhole(t *testing.T) {
	g := newGrid(6, 1)
	drawLine(g, 0, 0, 6, Line{{Text: "a日b", Role: RoleText}}, false)
	if got := g.rowText(0); got != "a日b  " {
		t.Fatalf("row = %q", got)
	}
	g.Put(2, 0, "x", cellStyle{}) // over the continuation of 日
	if got := g.rowText(0); got != "a xb  " {
		t.Fatalf("overwritten half: %q", got)
	}
}

func TestGridRenderPlacesLikeTheGrid(t *testing.T) {
	g := newGrid(20, 3)
	drawFrame(g, 0, 0, 20, 3, Frame{Title: Line{{Text: "Agents", Role: RoleText}}, Focused: true, Hue: RoleAccent})
	drawLine(g, 1, 1, 18, Line{{Text: "日本 ok ", Role: RoleOK}, {Text: "x", Role: RoleError, Bold: true}}, true)
	out := g.render(newPainter(th))
	for _, m := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		if got := placed(out, 20, 3, m); got != g.plain() {
			t.Fatalf("method %v places:\n%s\nwant:\n%s", m, got, g.plain())
		}
	}
}

func TestLipColor(t *testing.T) {
	if lipColor(tcell.ColorDefault) != nil {
		t.Fatal("default colour must be none")
	}
	r, g, b, _ := lipColor(tcell.GetColor("#7CC4FA")).RGBA()
	if r>>8 != 0x7c || g>>8 != 0xc4 || b>>8 != 0xfa {
		t.Fatalf("rgb = %x %x %x", r>>8, g>>8, b>>8)
	}
	if c, ok := lipColor(tcell.GetColor("teal")).(ansi.IndexedColor); !ok || c != 6 {
		t.Fatalf("teal = %#v", lipColor(tcell.GetColor("teal")))
	}
}

// The painter resolves a cell exactly as the tcell sink does.
func TestLipStyleMatchesTcellStyle(t *testing.T) {
	for _, pal := range []struct {
		name   string
		noCol  bool
		colors int
	}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		tt, _ := ResolveTheme(pal.name, "", "", pal.noCol, pal.colors)
		for _, c := range []cellStyle{{Role: RoleAccent}, {Role: RoleOK, Bold: true}, {Role: RoleText, Selected: true},
			{Role: RoleAttention, Fade: 40}, {Role: RoleDispatch, Pill: true}, {Role: RoleOK, Border: true}, {Role: RoleOK, Border: true, Focused: true}} {
			fg, bg, attr := tt.tcellStyle(c).Decompose()
			s := tt.lipStyle(c)
			if !sameColor(s.GetForeground(), fg) || !sameColor(s.GetBackground(), bg) ||
				s.GetBold() != (attr&tcell.AttrBold != 0) || s.GetReverse() != (attr&tcell.AttrReverse != 0) {
				t.Errorf("%s/%d %+v: lipgloss %v/%v bold %v rev %v, tcell %v/%v %v", pal.name, pal.colors, c,
					s.GetForeground(), s.GetBackground(), s.GetBold(), s.GetReverse(), fg, bg, attr)
			}
		}
	}
}

func sameColor(lc interface{ RGBA() (r, g, b, a uint32) }, tc tcell.Color) bool {
	want := lipColor(tc)
	if want == nil {
		_, isNo := lc.(lipgloss.NoColor)
		return isNo
	}
	r1, g1, b1, a1 := lc.RGBA()
	r2, g2, b2, a2 := want.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}
