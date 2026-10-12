package main

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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

// Theme is immutable after resolution. It never changes tview's global styles.
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

func (t *Theme) Selected(r Role) tcell.Style {
	s := t.Style(r)
	if t.palette == PaletteNone || t.palette == Palette16 {
		return s.Reverse(true)
	}
	return s.Background(t.selection)
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

func (t *Theme) Tag(r Role, s string) string {
	s = tview.Escape(s)
	if t.palette == PaletteNone || t.tags[themeRole(r)] == "-" {
		return s
	}
	return "[" + t.tags[themeRole(r)] + "]" + s + "[-]"
}

func (t *Theme) StyleInput(f *tview.InputField) {
	f.SetBackgroundColor(tcell.ColorDefault)
	f.SetBorderStyle(t.Border(false))
	f.SetTitleColor(t.colors[RoleText])
	f.SetFieldStyle(t.Style(RoleText))
	f.SetLabelStyle(t.Style(RoleAccent))
	f.SetPlaceholderStyle(t.Style(RoleMuted))
	f.SetAutocompleteStyles(tcell.ColorDefault, t.Style(RoleText), t.Selected(RoleText))
	f.SetAutocompleteUseTags(false)
}

func (t *Theme) StyleTextView(v *tview.TextView) {
	v.SetBackgroundColor(tcell.ColorDefault)
	v.SetTextStyle(t.Style(RoleText))
	v.SetBorderStyle(t.Border(false))
	v.SetTitleColor(t.colors[RoleText])
}

var th *Theme

func init() { th, _ = ResolveTheme("dark", "", "", false, 1<<24) }

func tg(r Role, s string) string { return th.Tag(r, s) }

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
	GlyphPrompt    = "›"
	GlyphHuman     = "♙" // a person on the bus (busmon), never an agent
	// Border glyphs are kept here too; Task 10 owns tview.Borders.
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
}
