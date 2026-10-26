package main

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gdamore/tcell/v2"
)

// The shell's View paints onto a grid of cells with the same geometry tview
// draws with (drawLine, drawFrame), then turns each row into styled spans
// (busmon Charm spec, step b). lipgloss styles spans; it never lays boxes out,
// so a frame cannot wrap by one cell.

// gridCell is one cell: a grapheme cluster, or the continuation of the wide
// cluster to its left (cont).
type gridCell struct {
	text string
	st   cellStyle
	cont bool
}

// grid is a cellSink of w x h cells, blank to start with.
type grid struct {
	w, h  int
	cells []gridCell
}

func newGrid(w, h int) *grid {
	g := &grid{w: max(0, w), h: max(0, h), cells: make([]gridCell, max(0, w)*max(0, h))}
	for i := range g.cells {
		g.cells[i] = gridCell{text: " "}
	}
	return g
}

func (g *grid) Size() (int, int) { return g.w, g.h }

func (g *grid) Put(x, y int, cluster string, st cellStyle) {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return
	}
	// A cluster written over half of a wide one blanks the other half: the
	// grid never holds a continuation without its head.
	if c := g.cells[y*g.w+x]; c.cont && x > 0 && cluster != "" {
		g.cells[y*g.w+x-1] = gridCell{text: " ", st: g.cells[y*g.w+x-1].st}
	}
	if cluster == "" {
		g.cells[y*g.w+x] = gridCell{st: st, cont: true}
		return
	}
	if x+1 < g.w && g.cells[y*g.w+x+1].cont && Width(cluster) < 2 {
		g.cells[y*g.w+x+1] = gridCell{text: " ", st: g.cells[y*g.w+x+1].st}
	}
	g.cells[y*g.w+x] = gridCell{text: cluster, st: st}
}

func (g *grid) at(x, y int) gridCell { return g.cells[y*g.w+x] }

// rowText is row y as the person reads it, without styles.
func (g *grid) rowText(y int) string {
	var sb strings.Builder
	for x := 0; x < g.w; x++ {
		if c := g.at(x, y); !c.cont {
			sb.WriteString(c.text)
		}
	}
	return sb.String()
}

// plain is the whole grid without styles, rows joined by "\n".
func (g *grid) plain() string {
	rows := make([]string, g.h)
	for y := range rows {
		rows[y] = g.rowText(y)
	}
	return strings.Join(rows, "\n")
}

// render is the grid as the View's content: each row's runs of one style
// rendered once by that style's lipgloss form.
func (g *grid) render(p *painter) string {
	var sb strings.Builder
	for y := 0; y < g.h; y++ {
		if y > 0 {
			sb.WriteByte('\n')
		}
		var run strings.Builder
		cur := cellStyle{}
		flush := func() {
			if run.Len() > 0 {
				sb.WriteString(p.style(cur).Render(run.String()))
				run.Reset()
			}
		}
		for x := 0; x < g.w; x++ {
			c := g.at(x, y)
			if c.cont {
				continue
			}
			if c.st != cur {
				flush()
				cur = c.st
			}
			run.WriteString(c.text)
		}
		flush()
	}
	return sb.String()
}

// painter holds the lipgloss style of each cellStyle for one theme, built on
// first use: the styles come from theme.go's palette only.
type painter struct {
	t      *Theme
	styles map[cellStyle]lipgloss.Style
}

func newPainter(t *Theme) *painter { return &painter{t: t, styles: map[cellStyle]lipgloss.Style{}} }

func (p *painter) style(c cellStyle) lipgloss.Style {
	if s, ok := p.styles[c]; ok {
		return s
	}
	s := p.t.lipStyle(c)
	p.styles[c] = s
	return s
}

// paint is one Line as styled text (no padding, no cut).
func (p *painter) paint(l Line) string {
	var sb strings.Builder
	for _, s := range l {
		sb.WriteString(p.style(cellStyle{Role: s.Role, Bold: s.Bold, Reverse: s.Reverse, Fade: s.Fade, Pill: s.Pill}).Render(s.Text))
	}
	return sb.String()
}

// lipStyle is a cell's style for the shell: the same resolution as
// tcellStyle, through the same palette (busmon Charm spec's Theme.Style(Role)
// lipgloss.Style; Theme.Style stays tcell's until step c removes tview).
func (t *Theme) lipStyle(c cellStyle) lipgloss.Style {
	return lipFrom(t.tcellStyle(c))
}

// lipFrom carries a tcell style's colours and attributes over to lipgloss.
func lipFrom(ts tcell.Style) lipgloss.Style {
	fg, bg, attr := ts.Decompose()
	s := lipgloss.NewStyle()
	if c := lipColor(fg); c != nil {
		s = s.Foreground(c)
	}
	if c := lipColor(bg); c != nil {
		s = s.Background(c)
	}
	if attr&tcell.AttrBold != 0 {
		s = s.Bold(true)
	}
	if attr&tcell.AttrReverse != 0 {
		s = s.Reverse(true)
	}
	return s
}

// lipColor is a tcell colour for lipgloss: true colour as itself, a palette
// colour as its ANSI index, the default as none.
func lipColor(c tcell.Color) color.Color {
	switch {
	case c == tcell.ColorDefault || !c.Valid():
		return nil
	case c.IsRGB():
		return lipgloss.Color(fmt.Sprintf("#%06x", c.Hex()))
	}
	return lipgloss.ANSIColor(uint8(c - tcell.ColorValid))
}
