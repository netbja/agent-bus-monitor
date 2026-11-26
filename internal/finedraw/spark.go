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
	lines, _ := SparkLines(vals, top, cells, 1, style)
	return lines[0]
}

// SparkLines is SparkWide on rows lines, top first: braille stacks its rows,
// 4 levels each, so 2 rows give 8 (a column's dots fill the bottom row
// first); block and ascii keep one row. share is each cell's highest value
// in percent of top (0 where the values do not reach), for the caller's
// colour.
func SparkLines(vals []float64, top float64, cells, rows int, style string) ([]string, []float64) {
	per := 1
	if style == StyleBraille {
		per = 2
	} else {
		rows = 1
	}
	rows = max(1, rows)
	var wide []float64
	if len(vals) > 0 {
		rep := max(1, per*cells/len(vals))
		for _, v := range vals {
			for range rep {
				wide = append(wide, v)
			}
		}
	}
	steps := SparkSteps
	if style == StyleASCII {
		steps = ASCIISteps
	}
	out := make([]strings.Builder, rows)
	share := make([]float64, cells)
	for c := range cells {
		i := c * per
		if i >= len(wide) {
			for r := range out {
				out[r].WriteByte(' ')
			}
			continue
		}
		hi := wide[i]
		if per == 2 && i+1 < len(wide) {
			hi = max(hi, wide[i+1])
		}
		if top > 0 {
			share[c] = hi / top * 100
		}
		if per == 1 {
			if l := Level(wide[i], top, len(steps)); l > 0 {
				out[0].WriteString(steps[l-1])
			} else {
				out[0].WriteByte(' ')
			}
			continue
		}
		left, right := Level(wide[i], top, 4*rows), 0
		if i+1 < len(wide) {
			right = Level(wide[i+1], top, 4*rows)
		}
		for r := range out {
			base := 4 * (rows - 1 - r) // the dots below this row
			out[r].WriteRune(BrailleCell(clamp(left-base, 0, 4), clamp(right-base, 0, 4)))
		}
	}
	lines := make([]string, rows)
	for r := range out {
		lines[r] = out[r].String()
	}
	return lines, share
}
