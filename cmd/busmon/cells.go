package main

import (
	"github.com/charmbracelet/x/ansi"
	"strings"

	"github.com/rivo/uniseg"
)

type Seg struct {
	Text    string
	Role    Role
	Bold    bool
	Reverse bool // the NO_COLOR stand-in for a highlight (active tab)
	Fade    int  // 1-100: drawn at that intensity, between RoleLine and its colour (true colour only); 0 = full
	Pill    bool // drawn on its role's colour (a focused frame's title)
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
	return agreeWidths(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		if removedControl(r) {
			return -1
		}
		return r
	}, s))
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
	s = agreeWidths(s)
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

// Width is busmon's one measure of text in cells (the spec's cellWidth):
// uniseg grapheme clusters, used by Fit, FitLine, frames and the layout.
func Width(s string) int { return uniseg.StringWidth(s) }

// widthReplacement stands for a cluster no width method agrees on.
const widthReplacement = "?"

// agreeWidths makes text measure the same under every method that will
// place it (busmon Charm spec, "Width: one method, enforced"): busmon's own
// (uniseg), lipgloss's (grapheme clusters) and bubbletea's renderer on a
// terminal without Unicode mode 2027 (wcwidth). A cluster they disagree on
// (an emoji with a variation selector, a ZWJ sequence, a skin tone) keeps its
// first rune when they agree on that, and is replaced otherwise; anything
// else is left as written.
func agreeWidths(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var out strings.Builder
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		c := g.Str()
		if widthsAgree(c) {
			out.WriteString(c)
			continue
		}
		if r := g.Runes(); len(r) > 1 && widthsAgree(string(r[0])) && Width(string(r[0])) > 0 {
			out.WriteRune(r[0])
			continue
		}
		out.WriteString(widthReplacement)
	}
	return out.String()
}

func widthsAgree(c string) bool {
	u := uniseg.StringWidth(c)
	return ansi.StringWidth(c) == u && ansi.StringWidthWc(c) == u
}

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
	if n := len(l); n > 0 && l[n-1].Role == style.Role && l[n-1].Bold == style.Bold && l[n-1].Reverse == style.Reverse && l[n-1].Fade == style.Fade && l[n-1].Pill == style.Pill {
		l[n-1].Text += text
		return l
	}
	return append(l, Seg{Text: text, Role: style.Role, Bold: style.Bold, Reverse: style.Reverse, Fade: style.Fade, Pill: style.Pill})
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
	// The padding keeps the last cell's colour but never its reverse: a
	// cursor cell at a row's end would light the rest of the row (A1).
	last.Reverse = false
	return appendSegment(out, strings.Repeat(" ", w-used), last)
}

// cellStyle is how one cell is drawn, independent of the terminal library
// (busmon Charm spec, step b): a role and its modifiers, or a frame line in a
// panel's hue. The painter resolves it from the theme (Theme.lipStyle, through
// tcellStyle's colours).
type cellStyle struct {
	Role     Role
	Bold     bool
	Reverse  bool
	Fade     int
	Pill     bool
	Selected bool
	Border   bool // a frame line: Theme.FrameBorder(Role, Focused)
	Focused  bool
}

// borderCell is the style of a frame line in hue, focused or not.
func borderCell(hue Role, focused bool) cellStyle {
	return cellStyle{Role: hue, Border: true, Focused: focused}
}

// cellSink is a grid of cells: the shell's grid, or a test's recording one.
type cellSink interface {
	Size() (w, h int)
	// Put writes one grapheme cluster at (x, y); "" is the continuation cell
	// of the wide cluster to its left.
	Put(x, y int, cluster string, st cellStyle)
}

func drawLine(s cellSink, x, y, w int, l Line, selected bool) {
	sw, sh := s.Size()
	if w <= 0 || y < 0 || y >= sh {
		return
	}
	pos := x
	lineClusters(FitLine(l, w), func(text string, width int, seg Seg) bool {
		st := cellStyle{Role: seg.Role, Bold: seg.Bold, Reverse: seg.Reverse, Fade: seg.Fade, Pill: seg.Pill, Selected: selected}
		// A wide cluster's continuation cells first, then its head.
		if width > 0 {
			whole := pos >= 0 && pos+width <= sw
			for off := width - 1; off >= 0; off-- {
				if cx := pos + off; cx >= 0 && cx < sw {
					switch {
					case off == 0 && whole:
						s.Put(cx, y, text, st)
					case whole:
						s.Put(cx, y, "", st)
					default:
						s.Put(cx, y, " ", st)
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
	Hue          Role // the panel's colour: its border and title (RoleAccent when unset)
}

// frameTitle pads the title with one space each side and colours its name in
// the panel's hue: plain when the frame is not focused, a pill when it is. A
// name is a plain-text segment or a bold one (the active tab); inactive tabs
// (their panel's colour, not bold) and coloured status words keep their own look. The pill takes the
// spaces around the name from its neighbours, so the width never changes
// (tab click spans are computed from it).
func frameTitle(t Line, hue Role, focused bool) Line {
	if t.Width() == 0 {
		return nil
	}
	name := func(s Seg) bool {
		return strings.TrimSpace(s.Text) != "" && (s.Role == RoleText || (s.Bold && s.Role != RoleMuted))
	}
	l := append(append(Line{{Text: " ", Role: RoleText}}, t...), Seg{Text: " ", Role: RoleText})
	var out Line
	for i := 0; i < len(l); i++ {
		s := l[i]
		inner := i > 0 && i < len(l)-1
		if !inner || !name(s) {
			out = append(out, s)
			continue
		}
		s.Role = hue
		if !focused {
			out = append(out, s)
			continue
		}
		s.Pill, s.Bold = true, true
		// Borrow one leading space from the previous segment, one trailing
		// space from the next, when they have one to give.
		if n := len(out); n > 0 && strings.HasSuffix(out[n-1].Text, " ") && !strings.HasPrefix(s.Text, " ") {
			out[n-1].Text = strings.TrimSuffix(out[n-1].Text, " ")
			s.Text = " " + s.Text
			if out[n-1].Text == "" {
				out = out[:n-1]
			}
		}
		out = append(out, s)
		if !strings.HasSuffix(s.Text, " ") && strings.HasPrefix(l[i+1].Text, " ") {
			out[len(out)-1].Text += " "
			l[i+1].Text = strings.TrimPrefix(l[i+1].Text, " ")
		}
	}
	var kept Line
	for _, s := range out {
		if s.Text != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

func drawFrame(s cellSink, x, y, w, h int, f Frame) {
	if w < 2 || h < 2 {
		return
	}
	sw, sh := s.Size()
	hue := f.Hue
	if hue == RoleText {
		hue = RoleAccent
	}
	style := borderCell(hue, f.Focused)
	put := func(px, py int, g string) {
		if px >= 0 && px < sw && py >= 0 && py < sh {
			s.Put(px, py, g, style)
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
	title := frameTitle(f.Title, hue, f.Focused)
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
			title[i].Bold = title[i].Role != RoleMuted // inactive tabs stay plain
		}
	}
	drawLine(s, x+2, y, titleW, title, false)
	drawLine(s, x+w-2-labelW, y, labelW, label, false)
	drawScrollbar(s, x+w-1, y, h, f.Scroll, style)
}

func drawScrollbar(s cellSink, x, y, h int, sc ScrollState, style cellStyle) {
	if sc.Total <= sc.Visible || sc.Total <= 0 {
		return
	}
	sw, sh := s.Size()
	put := func(px, py int, g string) {
		if px >= 0 && px < sw && py >= 0 && py < sh {
			s.Put(px, py, g, style)
		}
	}
	inner := h - 2
	if inner < 2 {
		return
	} // Preserve corners; need distinct arrow cells.
	put(x, y+1, GlyphBlocked)
	put(x, y+h-2, GlyphDown)
	track := inner - 2
	if track <= 0 {
		return
	}
	visible := max(0, sc.Visible)
	thumb := max(1, min(track, track*visible/sc.Total))
	maxTop := max(1, sc.Total-visible)
	top := min(max(0, sc.Top), maxTop)
	start := (track - thumb) * top / maxTop
	for i := 0; i < thumb; i++ {
		put(x, y+2+start+i, glyphThumb)
	}
}

// CopyText is what `y` copies: the stored text with controls and bidi
// overrides removed (as on screen), newlines and tabs kept as written.
func CopyText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == '\r' || removedControl(r) {
			return -1
		}
		return r
	}, s)
}
