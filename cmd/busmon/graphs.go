package main

import (
	"strings"

	"github.com/netbja/agent-bus-monitor/config"
)

// The sparklines' styles (braille-sparklines, spec
// docs/superpowers/specs/2026-11-18-braille-sparklines-design.md): the
// display.graphs setting, auto resolved by graphsStyle. Gauges and bars keep
// their blocks whatever the style.
const (
	graphsAuto    = "auto"
	graphsBlock   = "block"
	graphsBraille = "braille"
	graphsASCII   = "ascii"
)

// sparkSteps are the eight blocks, lowest first; asciiSteps the same eight
// levels in ASCII.
var (
	sparkSteps = []string{GlyphSpark1, GlyphSpark2, GlyphSpark3, GlyphSpark4, GlyphSpark5, GlyphSpark6, GlyphGauge, GlyphBarFull}
	asciiSteps = []string{"_", ".", "-", ":", "=", "+", "*", "#"}
)

// brailleBlank is the empty braille cell, so a column keeps its width in
// every font; brailleLeft and brailleRight are each column's dots, bottom
// first (§4's table).
const brailleBlank rune = 0x2800 // U+2800 as a number: the UI glyph set stays closed (TestBrailleWidth pins each cell at one)

var (
	brailleLeft  = [4]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}
)

// graphsStyle resolves display.graphs: an explicit block, braille or ascii
// wins; auto (and an unknown or empty value) is ascii when the locale is not
// UTF-8, else block. auto never gives braille (§2).
func graphsStyle(setting string, getenv func(string) string) string {
	switch setting {
	case graphsBlock, graphsBraille, graphsASCII:
		return setting
	}
	l := strings.ToLower(config.Locale(getenv, "LC_CTYPE"))
	if !strings.Contains(l, "utf-8") && !strings.Contains(l, "utf8") {
		return graphsASCII
	}
	return graphsBlock // TERM=linux included: its fonts have the blocks
}

// sparkLevel is v on n levels of max: 0 for none, else 1 to n, so any
// non-zero value shows at least the lowest step.
func sparkLevel(v, max float64, n int) int {
	if v <= 0 || max <= 0 {
		return 0
	}
	return clamp(int(v/max*float64(n)), 0, n-1) + 1
}

// brailleCell is one cell of two days: left and right are levels 0 to 4,
// each lighting its column's dots from the bottom.
func brailleCell(left, right int) rune {
	c := brailleBlank
	for i := range left {
		c |= brailleLeft[i]
	}
	for i := range right {
		c |= brailleRight[i]
	}
	return c
}

// brailleLevels reads a cell's two levels back (the round trip of the tests).
func brailleLevels(c rune) (left, right int) {
	for i := range 4 {
		if c&brailleLeft[i] != 0 {
			left = i + 1
		}
		if c&brailleRight[i] != 0 {
			right = i + 1
		}
	}
	return left, right
}

// sparkLine draws vals on the scale of max in style, one cell per value
// (block, ascii) or per two values (braille, 4 levels, filled), padded to
// len(vals) cells so what follows stays aligned (§3). A value of none is a
// space, or the blank braille cell.
func sparkLine(vals []float64, max float64, style string) string {
	var b strings.Builder
	if style != graphsBraille {
		steps := sparkSteps
		if style == graphsASCII {
			steps = asciiSteps
		}
		for _, v := range vals {
			if l := sparkLevel(v, max, len(steps)); l > 0 {
				b.WriteString(steps[l-1])
			} else {
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	cells := 0
	for i := 0; i < len(vals); i += 2 {
		right := 0
		if i+1 < len(vals) {
			right = sparkLevel(vals[i+1], max, 4)
		}
		b.WriteRune(brailleCell(sparkLevel(vals[i], max, 4), right))
		cells++
	}
	b.WriteString(strings.Repeat(" ", len(vals)-cells))
	return b.String()
}

// sparkWide draws vals over cells (tokens-layout): each value repeated so
// the line fills them, one point per cell (block, ascii) or two (braille),
// on the scale of top; spaces where the values do not reach. No values is
// cells spaces.
func sparkWide(vals []float64, top float64, cells int, style string) string {
	points := cells
	if style == graphsBraille {
		points = 2 * cells
	}
	var wide []float64
	if len(vals) > 0 {
		rep := max(1, points/len(vals))
		for _, v := range vals {
			for range rep {
				wide = append(wide, v)
			}
		}
	}
	r := []rune(sparkLine(wide, top, style))
	if len(r) > cells { // braille pads to one cell per value: keep the drawn cells
		r = r[:cells]
	}
	return string(r) + strings.Repeat(" ", cells-len(r))
}
