package main

import "strconv"

// Color is a terminal colour as the theme keeps it: the terminal's own (the
// zero value), one of the 16 ANSI colours by index, or true colour. It took
// the place of tcell's Color when busmon left tcell (the painter has drawn
// through lipgloss since the Charm port); the semantics are tcell's, so the
// goldens did not move.
type Color struct {
	kind colorKind
	v    uint32 // the ANSI index, or 0xRRGGBB
}

type colorKind uint8

const (
	colorKindDefault colorKind = iota
	colorKindIndex
	colorKindRGB
)

// colorDefault is the terminal's own colour.
var colorDefault = Color{}

// ansiNames are the 16 ANSI colours by their W3C names, as the 16-colour
// palette and providerHues write them.
var ansiNames = map[string]uint32{
	"black": 0, "maroon": 1, "green": 2, "olive": 3, "navy": 4, "purple": 5, "teal": 6, "silver": 7,
	"gray": 8, "red": 9, "lime": 10, "yellow": 11, "blue": 12, "fuchsia": 13, "aqua": 14, "white": 15,
}

// getColor is a palette name or a "#rrggbb" string as a colour; anything else
// is the terminal's own.
func getColor(name string) Color {
	if i, ok := ansiNames[name]; ok {
		return Color{kind: colorKindIndex, v: i}
	}
	if len(name) == 7 && name[0] == '#' {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			return Color{kind: colorKindRGB, v: uint32(v)}
		}
	}
	return colorDefault
}

// newRGBColor is a true colour from its components (0-255 each).
func newRGBColor(r, g, b int32) Color {
	return Color{kind: colorKindRGB, v: uint32(r&0xff)<<16 | uint32(g&0xff)<<8 | uint32(b&0xff)}
}

// RGB is a true colour's components; -1 each for any other colour, which has
// no fixed value (the theme fades only true colours).
func (c Color) RGB() (r, g, b int32) {
	if c.kind != colorKindRGB {
		return -1, -1, -1
	}
	return int32(c.v >> 16 & 0xff), int32(c.v >> 8 & 0xff), int32(c.v & 0xff)
}

// index is an ANSI colour's index.
func (c Color) index() (int, bool) { return int(c.v), c.kind == colorKindIndex }

// attrMask is a style's attributes.
type attrMask uint8

const (
	attrBold attrMask = 1 << iota
	attrReverse
)

// Style is a cell's colours and attributes, a value: each setter returns a
// copy. It replaces tcell's Style; the painter turns it into lipgloss.
type Style struct {
	fg, bg Color
	attr   attrMask
}

// styleDefault is the terminal's own colours, no attribute.
var styleDefault = Style{}

func (s Style) Foreground(c Color) Style { s.fg = c; return s }
func (s Style) Background(c Color) Style { s.bg = c; return s }
func (s Style) Bold(on bool) Style       { return s.set(attrBold, on) }
func (s Style) Reverse(on bool) Style    { return s.set(attrReverse, on) }

func (s Style) set(a attrMask, on bool) Style {
	if on {
		s.attr |= a
	} else {
		s.attr &^= a
	}
	return s
}

// Decompose is the style's foreground, background and attributes.
func (s Style) Decompose() (fg, bg Color, attr attrMask) { return s.fg, s.bg, s.attr }
