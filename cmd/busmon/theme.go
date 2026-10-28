package main

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

type Role int

const (
	RoleText Role = iota
	RoleMuted
	RoleDim
	RoleAccent
	RoleOK
	RoleAttention
	RoleError
	RoleDone
	RoleDispatch
	RoleLine
)

type Palette int

const (
	PaletteDark Palette = iota
	PaletteLight
	Palette16
	PaletteNone
)

// Theme is immutable after resolution.
type Theme struct {
	palette   Palette
	colors    [10]tcell.Color
	tags      [10]string
	selection tcell.Color
}

func ResolveTheme(flagVal, envVal, colorfgbg string, noColor bool, colors int) (*Theme, error) {
	name := flagVal
	if name == "" {
		name = envVal
	}
	if name == "" {
		name = "auto"
	}
	if name != "auto" && name != "dark" && name != "light" {
		return nil, fmt.Errorf("invalid theme %q (want auto, dark or light)", name)
	}
	if name == "auto" {
		name = "dark"
		parts := strings.Split(colorfgbg, ";")
		bg := strings.TrimSpace(parts[len(parts)-1])
		if bg == "7" || bg == "15" {
			name = "light"
		}
	}
	t := &Theme{palette: PaletteDark, selection: tcell.GetColor("#22324A")}
	t.tags = [10]string{"#E6EDF3", "#7D8A99", "#4A5563", "#7CC4FA", "#7EE2A8", "#E8BC63", "#F08A8A", "#9DA7F5", "#D7A6F5", "#2E3A48"}
	if name == "light" {
		t.palette = PaletteLight
		t.selection = tcell.GetColor("#DDF4FF")
		t.tags = [10]string{"#1F2328", "#5A6572", "#B0B8C1", "#0969DA", "#1A7F37", "#9A6700", "#CF222E", "#5A4FCF", "#8250DF", "#D0D7DE"}
	}
	if colors < 1<<24 {
		t.palette = Palette16
		t.tags = [10]string{"-", "silver", "gray", "teal", "green", "olive", "red", "blue", "purple", "gray"}
	}
	if noColor {
		t.palette = PaletteNone
	}
	for r, name := range t.tags {
		t.tags[r] = strings.ToLower(name)
		if t.palette == PaletteNone || name == "-" {
			t.colors[r] = tcell.ColorDefault
		} else {
			t.colors[r] = tcell.GetColor(name)
		}
	}
	return t, nil
}

func (t *Theme) Palette() Palette { return t.palette }

func themeRole(r Role) Role {
	if r < RoleText || r > RoleLine {
		return RoleText
	}
	return r
}

func (t *Theme) Style(r Role) tcell.Style {
	return tcell.StyleDefault.Foreground(t.colors[themeRole(r)]).Background(tcell.ColorDefault)
}

// Faded is r's colour at pct percent of the way from the frame line colour
// (close to the background) to r: a fade. Without true colour there are no
// shades to step through, so the colour stays whole (no animation).
func (t *Theme) Faded(r Role, pct int) tcell.Color {
	c := t.colors[themeRole(r)]
	if t.palette != PaletteDark && t.palette != PaletteLight {
		return c
	}
	pct = max(0, min(100, pct))
	r1, g1, b1 := t.colors[RoleLine].RGB()
	r2, g2, b2 := c.RGB()
	mix := func(a, b int32) int32 { return a + (b-a)*int32(pct)/100 }
	return tcell.NewRGBColor(mix(r1, r2), mix(g1, g2), mix(b1, b2))
}

// Animated: the palette has the shades a fade needs.
func (t *Theme) Animated() bool { return t.palette == PaletteDark || t.palette == PaletteLight }

func (t *Theme) Selected(r Role) tcell.Style {
	s := t.Style(r)
	if t.palette == PaletteNone || t.palette == Palette16 {
		return s.Reverse(true)
	}
	return s.Background(t.selection)
}

// FrameBorder is a panel frame in its own colour: whole when the panel has
// the focus, faded towards the background when it does not (true colour;
// the 16-colour and no-colour palettes keep the plain line).
func (t *Theme) FrameBorder(hue Role, focused bool) tcell.Style {
	if focused {
		s := t.Style(hue)
		if t.palette == PaletteNone {
			s = s.Bold(true)
		}
		return s
	}
	if t.Animated() {
		return tcell.StyleDefault.Foreground(t.Faded(hue, frameFade)).Background(tcell.ColorDefault)
	}
	return t.Style(RoleLine)
}

// frameFade is how much of its colour an unfocused frame keeps.
const frameFade = 35

// Pill is text on its role's colour: a focused frame's title. Without true
// colour it is the reverse of the role's colour.
func (t *Theme) Pill(r Role) tcell.Style {
	switch t.palette {
	case PaletteDark:
		return tcell.StyleDefault.Foreground(tcell.GetColor("#0D1117")).Background(t.colors[themeRole(r)]).Bold(true)
	case PaletteLight:
		return tcell.StyleDefault.Foreground(tcell.GetColor("#FFFFFF")).Background(t.colors[themeRole(r)]).Bold(true)
	}
	return t.Style(r).Reverse(true).Bold(true)
}

// tcellStyle is a cell's style on a tcell screen.
func (t *Theme) tcellStyle(c cellStyle) tcell.Style {
	if c.Border {
		return t.FrameBorder(c.Role, c.Focused)
	}
	style := t.Style(c.Role)
	if c.Selected {
		style = t.Selected(c.Role)
	}
	if c.Fade > 0 {
		style = style.Foreground(t.Faded(c.Role, c.Fade))
	}
	if c.Bold {
		style = style.Bold(true)
	}
	if c.Reverse {
		style = style.Reverse(true)
	}
	if c.Pill { // replaces the style whole: a pill has no fade; NO_COLOR keeps its reverse in Pill
		style = t.Pill(c.Role)
	}
	return style
}

func (t *Theme) Border(focused bool) tcell.Style {
	if focused {
		s := t.Style(RoleAccent)
		if t.palette == PaletteNone {
			s = s.Bold(true)
		}
		return s
	}
	return t.Style(RoleLine)
}

var th *Theme

func init() { th, _ = ResolveTheme("dark", "", "", false, 1<<24) }

const (
	GlyphWorking   = "●"
	GlyphIdle      = "○"
	GlyphBlocked   = "▲"
	GlyphDown      = "▼"
	GlyphDone      = "✓"
	GlyphNone      = "·"
	GlyphSelect    = "▶"
	GlyphArrowR    = "→"
	GlyphArrowL    = "←"
	GlyphUp        = "↑"
	GlyphDownArrow = "↓"
	GlyphEllipsis  = "…"
	GlyphDash      = "—"
	GlyphBarFull   = "█"
	GlyphBarEmpty  = "░"
	GlyphGauge     = "▇"
	// The updated column's freshness gauge: the same two dots as working/idle.
	GlyphDotFull     = GlyphWorking
	GlyphDotEmpty    = GlyphIdle
	GlyphPrompt      = "›"
	GlyphHuman       = "♙" // a person on the bus (busmon), never an agent
	GlyphNotEqual    = "≠" // model≠: the running model is not the configured one
	GlyphGate        = "◆" // a 4-eyes gate waiting on a verdict (For you); ▲ means blocked
	GlyphReturn      = "↵" // an expansion's last line: Enter again opens the full text
	glyphVertical    = "│"
	glyphThumb       = "┃"
	glyphHorizontal  = "─"
	glyphTopLeft     = "╭"
	glyphTopRight    = "╮"
	glyphBottomLeft  = "╰"
	glyphBottomRight = "╯"
)

var UIGlyphs = []string{
	GlyphWorking, GlyphIdle, GlyphBlocked, GlyphDown, GlyphDone, GlyphNone,
	GlyphSelect, GlyphArrowR, GlyphArrowL, GlyphUp, GlyphDownArrow, GlyphEllipsis,
	GlyphDash, GlyphBarFull, GlyphBarEmpty, GlyphGauge, glyphVertical, glyphThumb,
	glyphHorizontal, glyphTopLeft, glyphTopRight, glyphBottomLeft, glyphBottomRight, GlyphPrompt, GlyphHuman,
	GlyphNotEqual, GlyphGate, GlyphReturn,
}
