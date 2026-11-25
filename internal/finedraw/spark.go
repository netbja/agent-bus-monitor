package finedraw

import "strings"

// The sparklines' styles (braille-sparklines, spec
// docs/superpowers/specs/2026-11-18-braille-sparklines-design.md).
const (
	StyleBlock   = "block"
	StyleBraille = "braille"
	StyleASCII   = "ascii"
)

// SparkSteps are the eight blocks, lowest first; ASCIISteps the same eight
// levels in ASCII.
var (
	SparkSteps = []string{Spark1, Spark2, Spark3, Spark4, Spark5, Spark6, Gauge, Full}
	ASCIISteps = []string{"_", ".", "-", ":", "=", "+", "*", "#"}
)

// BrailleBlank is the empty braille cell, so a column keeps its width in
// every font; brailleLeft and brailleRight are each column's dots, bottom
// first.
const BrailleBlank rune = 0x2800

var (
	brailleLeft  = [4]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}
)

// Level is v on n levels of max: 0 for none, else 1 to n, so any non-zero
// value shows at least the lowest step.
func Level(v, max float64, n int) int {
	if v <= 0 || max <= 0 {
		return 0
	}
	return clamp(int(v/max*float64(n)), 0, n-1) + 1
}

// BrailleCell is one cell of two samples: left and right are levels 0 to 4,
// each lighting its column's dots from the bottom.
func BrailleCell(left, right int) rune {
	c := BrailleBlank
	for i := range left {
		c |= brailleLeft[i]
	}
	for i := range right {
		c |= brailleRight[i]
	}
	return c
}

// BrailleLevels reads a cell's two levels back.
func BrailleLevels(c rune) (left, right int) {
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

// Spark draws vals on the scale of max in style, one cell per value (block,
// ascii) or per two values (braille, 4 levels, filled), padded to len(vals)
// cells so what follows stays aligned. A value of none is a space, or the
// blank braille cell.
func Spark(vals []float64, max float64, style string) string {
	var b strings.Builder
	if style != StyleBraille {
		steps := SparkSteps
		if style == StyleASCII {
			steps = ASCIISteps
		}
		for _, v := range vals {
			if l := Level(v, max, len(steps)); l > 0 {
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
			right = Level(vals[i+1], max, 4)
		}
		b.WriteRune(BrailleCell(Level(vals[i], max, 4), right))
		cells++
	}
	b.WriteString(strings.Repeat(" ", len(vals)-cells))
	return b.String()
}

// SparkWide draws vals over cells: each value repeated so the line fills
// them, one point per cell (block, ascii) or two (braille), on the scale of
// top; spaces where the values do not reach. No values is cells spaces.
func SparkWide(vals []float64, top float64, cells int, style string) string {
	points := cells
	if style == StyleBraille {
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
	r := []rune(Spark(wide, top, style))
	if len(r) > cells { // braille pads to one cell per value: keep the drawn cells
		r = r[:cells]
	}
	return string(r) + strings.Repeat(" ", cells-len(r))
}
