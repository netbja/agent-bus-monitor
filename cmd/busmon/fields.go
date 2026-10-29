package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// fields is a block of a page's "Label:  value" rows (busmon-detail-labels,
// the person): each label capitalised with a colon, every value of the block in
// one column two cells after the longest label; a value too long for the
// line wraps under its value, never under the label. An empty label
// continues the row above; a gap is a blank line inside the same block, so
// the columns stay one.
type fields struct{ rows []fieldRow }

type fieldRow struct {
	label string
	value []Seg
	gap   bool
}

func (f *fields) add(label string, v ...Seg) {
	f.rows = append(f.rows, fieldRow{label: label, value: v})
}

func (f *fields) gap() { f.rows = append(f.rows, fieldRow{gap: true}) }

// fieldLabel is "last fact" as "Last fact:"; "" stays "".
func fieldLabel(s string) string {
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:] + ":"
}

// fields writes the block at width (the page's own; mdWidth for reader
// pages), each row ending its line; an empty block writes nothing.
func (b *body) fields(f *fields, width int) *body {
	if len(f.rows) == 0 {
		return b
	}
	return b.join(f.lines(width)).text("\n")
}

// lines are the block's rows at width: a value that fits keeps its segments
// as built; a longer one is wrapped word by word under the value column.
func (f *fields) lines(width int) []Line {
	col := 0
	for _, r := range f.rows {
		col = max(col, Width(fieldLabel(r.label)))
	}
	col += 2
	hang := strings.Repeat(" ", col)
	var out []Line
	for _, r := range f.rows {
		if r.gap {
			out = append(out, nil)
			continue
		}
		label := Fit(fieldLabel(r.label), col)
		value := Line(r.value)
		if col+value.Width() <= width {
			out = append(out, append(Line{S(RoleMuted, label)}, value...))
			continue
		}
		spans := make([]mdSpan, 0, len(r.value))
		for _, s := range r.value {
			spans = append(spans, mdSpan{text: s.Text, role: s.Role, bold: s.Bold})
		}
		out = append(out, wrapSpans(spans, width, label, hang)...)
	}
	return out
}
