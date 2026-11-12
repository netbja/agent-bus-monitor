// Package screen is the internal seam over the terminal emulator (herdr exit
// step 4, spec 2026-11-10-herdr-exit-design.md 6.1 and 6.4): the holder feeds
// a terminal's output to a Screen and reads back what a person would see.
// The emulator behind it was chosen by a bake-off (BAKEOFF.md); the seam lets
// it be swapped without touching the holder or agentstate.
//
// One cell model for every adapter: Plain and ANSI are rendered here, by
// this package, so agentstate's signatures (some of which match SGR dim, the
// Claude Code and Codex placeholders) read the same bytes whichever emulator
// produced the cells.
package screen

import (
	"strconv"
	"strings"
)

// Screen is one terminal's emulated screen.
type Screen interface {
	// Write feeds the program's output, in any chunking.
	Write(p []byte) (int, error)
	// Resize changes the screen's size, as the PTY's.
	Resize(cols, rows int)
	// Snapshot is the visible screen and, with lines > 0, up to that many
	// scrollback lines above it.
	Snapshot(lines int) Snapshot
	// Modes are the modes the program set.
	Modes() Modes
	// Render is a first paint: what makes an empty outer terminal show this
	// screen (clear, the lines with their styles, the cursor).
	Render() []byte
	// Replies takes what the emulator answered to the program's queries
	// (device attributes, cursor position, mode reports): the holder writes
	// them to the PTY, as a real terminal would.
	Replies() []byte
	Close() error
}

// New is the production emulator: a pure-Go screen of cols x rows keeping
// scrollback lines (the bake-off's choice, BAKEOFF.md).
func New(cols, rows, scrollback int) Screen { return newVT(cols, rows, scrollback) }

// Modes are the program-set modes the holder and the dispatcher need.
type Modes struct {
	AltScreen      bool // ?1049/?47/?1047: a full-screen program
	BracketedPaste bool // ?2004: a submit is pasted between ESC[200~ and ESC[201~
	CursorHidden   bool // ?25 reset
	SyncOutput     bool // ?2026: the program is between frames
}

// ColorKind tells a cell's color apart: the terminal's default, one of the
// 16 basic colors, the 256-color palette, or RGB.
type ColorKind uint8

const (
	ColorDefault ColorKind = iota
	ColorBasic
	ColorIndexed
	ColorRGB
)

// Color is a cell's foreground or background.
type Color struct {
	Kind    ColorKind
	N       uint8 // basic (0-15) or indexed (0-255)
	R, G, B uint8
}

// Attr is a cell's style.
type Attr struct {
	Bold, Dim, Italic, Underline, Reverse bool
	FG, BG                                Color
}

// Cell is one column of a line. A wide cluster is one cell of Width 2
// followed by one of Width 0 (its continuation, never rendered).
type Cell struct {
	Text  string
	Width int
	Attr  Attr
}

// Snapshot is a screen read: the scrollback lines asked for (oldest first,
// as rendered ANSI text: a cell-per-column line costs kilobytes, BAKEOFF.md),
// the visible lines top to bottom as cells, the size and the cursor on the
// visible screen.
type Snapshot struct {
	Cols, Rows       int
	Scrollback       []string
	Lines            [][]Cell
	CursorX, CursorY int
}

// stripSGR removes the SGR sequences a rendered line carries.
func stripSGR(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return b.String()
		}
		s = s[i+j+1:]
	}
}

// cellText is what a cell shows: an empty cell is a space.
func cellText(c Cell) string {
	if c.Width == 0 {
		return ""
	}
	if c.Text == "" {
		return " "
	}
	return c.Text
}

// Plain is the snapshot as text: one line per row joined by \n, each with
// its trailing spaces removed.
func (s Snapshot) Plain() string {
	out := make([]string, 0, len(s.Scrollback)+len(s.Lines))
	for _, l := range s.Scrollback {
		out = append(out, strings.TrimRight(stripSGR(l), " "))
	}
	for _, l := range s.Lines {
		var b strings.Builder
		for _, c := range l {
			b.WriteString(cellText(c))
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(out, "\n")
}

// sgr is the canonical SGR of a style: a reset, then one sequence per
// attribute, then the foreground and the background, each its own sequence,
// as the captured screens carry them (Codex's placeholder is a lone ESC[2m
// after its background: a merged ESC[2;48;…m would miss its signature).
// Equal styles always read the same bytes.
func sgr(a Attr) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	for _, x := range []struct {
		on   bool
		code string
	}{{a.Bold, "1"}, {a.Dim, "2"}, {a.Italic, "3"}, {a.Underline, "4"}, {a.Reverse, "7"}} {
		if x.on {
			b.WriteString("\x1b[" + x.code + "m")
		}
	}
	for _, c := range [][]string{colorCodes(a.FG, false), colorCodes(a.BG, true)} {
		if c != nil {
			b.WriteString("\x1b[" + strings.Join(c, ";") + "m")
		}
	}
	return b.String()
}

func colorCodes(c Color, bg bool) []string {
	base := 30
	if bg {
		base = 40
	}
	switch c.Kind {
	case ColorBasic:
		if c.N < 8 {
			return []string{strconv.Itoa(base + int(c.N))}
		}
		return []string{strconv.Itoa(base + 60 + int(c.N) - 8)}
	case ColorIndexed:
		return []string{strconv.Itoa(base + 8), "5", strconv.Itoa(int(c.N))}
	case ColorRGB:
		return []string{strconv.Itoa(base + 8), "2", strconv.Itoa(int(c.R)), strconv.Itoa(int(c.G)), strconv.Itoa(int(c.B))}
	}
	return nil
}

// ANSI is the snapshot with its styles: each style change is its canonical
// SGR, a styled line ends with ESC[0m, trailing unstyled spaces are removed.
// Lines are joined by \n.
func (s Snapshot) ANSI() string {
	out := append(make([]string, 0, len(s.Scrollback)+len(s.Lines)), s.Scrollback...)
	for _, l := range s.Lines {
		out = append(out, ansiLine(l))
	}
	return strings.Join(out, "\n")
}

// ansiLine is one line of cells in its canonical styles.
func ansiLine(l []Cell) string {
	end := len(l) // drop trailing unstyled blanks
	for end > 0 && l[end-1].Attr == (Attr{}) && (cellText(l[end-1]) == " " || l[end-1].Width == 0 && l[end-1].Text == "") {
		end--
	}
	var b strings.Builder
	cur := Attr{}
	for _, c := range l[:end] {
		if c.Width == 0 {
			continue
		}
		if c.Attr != cur {
			b.WriteString(sgr(c.Attr))
			cur = c.Attr
		}
		b.WriteString(cellText(c))
	}
	if cur != (Attr{}) {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
