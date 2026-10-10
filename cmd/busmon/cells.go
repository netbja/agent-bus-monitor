package main

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

type Seg struct {
	Text string
	Role Role
	Bold bool
}

type Line []Seg

func removedControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x061c ||
		r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069)
}

// Preview normalizes display text only. Keep the original for copying/filtering.
// Style tags are deliberately not interpreted or escaped at this stage.
func Preview(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		if removedControl(r) {
			return -1
		}
		return r
	}, s)
}

// DetailText preserves line structure and graphemes. Tab stops count cells of
// already-sanitized text, not bytes, runes, or style-tag interpretations.
func DetailText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == '\r' {
			return ' '
		}
		if removedControl(r) {
			return -1
		}
		return r
	}, s)
	var out strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		col := 0
		for j, part := range strings.Split(line, "\t") {
			if j > 0 {
				n := 4 - col%4
				out.WriteString(strings.Repeat(" ", n))
				col += n
			}
			out.WriteString(part)
			col += Width(part)
		}
	}
	return out.String()
}

func Width(s string) int { return uniseg.StringWidth(s) }

func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	var out strings.Builder
	used := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		if used+g.Width() > w-1 {
			break
		}
		out.WriteString(g.Str())
		used += g.Width()
	}
	return out.String() + GlyphEllipsis
}

func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = Truncate(s, w)
	return s + strings.Repeat(" ", w-Width(s))
}

func (l Line) text() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

func (l Line) Width() int { return Width(l.text()) }

// lineClusters joins before segmentation: even a combining mark or a ZWJ
// sequence split across segments stays indivisible. The cluster's first byte
// owns its style. Byte offsets here locate styles; they never measure cells.
func lineClusters(l Line, visit func(string, int, Seg) bool) {
	g := uniseg.NewGraphemes(l.text())
	idx, end := 0, 0
	if len(l) > 0 {
		end = len(l[0].Text)
	}
	for g.Next() {
		start, _ := g.Positions()
		for idx < len(l)-1 && start >= end {
			idx++
			end += len(l[idx].Text)
		}
		if !visit(g.Str(), g.Width(), l[idx]) {
			return
		}
	}
}

func appendSegment(l Line, text string, style Seg) Line {
	if text == "" {
		return l
	}
	if n := len(l); n > 0 && l[n-1].Role == style.Role && l[n-1].Bold == style.Bold {
		l[n-1].Text += text
		return l
	}
	return append(l, Seg{Text: text, Role: style.Role, Bold: style.Bold})
}

// FitLine pads AFTER the ellipsis, consistently with Fit. It never mutates l.
func FitLine(l Line, w int) Line {
	if w <= 0 {
		return nil
	}
	cut := l.Width() > w
	budget := w
	if cut {
		budget--
	}
	var out Line
	used := 0
	last := Seg{Role: RoleText}
	lineClusters(l, func(text string, width int, style Seg) bool {
		last = style
		if used+width > budget {
			return false
		}
		out = appendSegment(out, text, style)
		used += width
		return true
	})
	if cut {
		out = appendSegment(out, GlyphEllipsis, last)
		used++
	}
	return appendSegment(out, strings.Repeat(" ", w-used), last)
}

// DrawLine consumes normalized single-line text; it never parses tview tags.
// Wide clusters are written whole, including their continuation cells, and
// clipping to the physical screen never leaves half a cluster at an edge.
func DrawLine(s tcell.Screen, x, y, w int, l Line, selected bool) {
	sw, sh := s.Size()
	if w <= 0 || y < 0 || y >= sh {
		return
	}
	pos := x
	lineClusters(FitLine(l, w), func(text string, width int, seg Seg) bool {
		style := th.Style(seg.Role)
		if selected {
			style = th.Selected(seg.Role)
		}
		if seg.Bold {
			style = style.Bold(true)
		}
		// Match tview's continuation-cell write order.
		if width > 0 {
			runes := []rune(text)
			whole := pos >= 0 && pos+width <= sw
			for off := width - 1; off >= 0; off-- {
				if cx := pos + off; cx >= 0 && cx < sw {
					if off == 0 && whole {
						s.SetContent(cx, y, runes[0], runes[1:], style)
					} else {
						s.SetContent(cx, y, ' ', nil, style)
					}
				}
			}
		}
		pos += width
		return true
	})
}

type ScrollState struct{ Total, Top, Visible int }

type Frame struct {
	Title, Label Line
	Focused      bool
	Scroll       ScrollState
}

// DrawFrame draws only the perimeter and top labels; callers own the interior.
// A rectangle smaller than 2x2 has no room for four corners and draws nothing.
func DrawFrame(s tcell.Screen, x, y, w, h int, f Frame) {
	if w < 2 || h < 2 {
		return
	}
	sw, sh := s.Size()
	style := th.Border(f.Focused)
	put := func(px, py int, g string) {
		if px >= 0 && px < sw && py >= 0 && py < sh {
			s.SetContent(px, py, []rune(g)[0], nil, style)
		}
	}
	for dx := 1; dx < w-1; dx++ {
		put(x+dx, y, glyphHorizontal)
		put(x+dx, y+h-1, glyphHorizontal)
	}
	for dy := 1; dy < h-1; dy++ {
		put(x, y+dy, glyphVertical)
		put(x+w-1, y+dy, glyphVertical)
	}
	put(x, y, glyphTopLeft)
	put(x+w-1, y, glyphTopRight)
	put(x, y+h-1, glyphBottomLeft)
	put(x+w-1, y+h-1, glyphBottomRight)
	available := max(0, w-4) // x+2 through x+w-3 inclusive.
	var title Line
	if f.Title.Width() > 0 {
		title = append(title, Seg{Text: " ", Role: RoleText})
		title = append(title, f.Title...)
		title = append(title, Seg{Text: " ", Role: RoleText})
	}
	var label Line
	if f.Label.Width() > 0 {
		label = append(append(Line{{Text: " ", Role: RoleText}}, f.Label...), Seg{Text: " ", Role: RoleText})
	}
	titleW := min(title.Width(), available)
	labelRoom := available - titleW
	if titleW > 0 && labelRoom > 0 {
		labelRoom--
	}
	labelW := min(label.Width(), labelRoom)
	if f.Focused && th.Palette() == PaletteNone {
		for i := range title {
			title[i].Bold = true
		}
	}
	DrawLine(s, x+2, y, titleW, title, false)
	DrawLine(s, x+w-2-labelW, y, labelW, label, false)
	if f.Scroll.Total <= f.Scroll.Visible || f.Scroll.Total <= 0 {
		return
	}
	inner := h - 2
	if inner < 2 {
		return
	} // Preserve corners; need distinct arrow cells.
	put(x+w-1, y+1, GlyphBlocked)
	put(x+w-1, y+h-2, GlyphDown)
	track := inner - 2
	if track <= 0 {
		return
	}
	visible := max(0, f.Scroll.Visible)
	thumb := max(1, min(track, track*visible/f.Scroll.Total))
	maxTop := max(1, f.Scroll.Total-visible)
	top := min(max(0, f.Scroll.Top), maxTop)
	start := (track - thumb) * top / maxTop
	for i := 0; i < thumb; i++ {
		put(x+w-1, y+2+start+i, glyphThumb)
	}
}
