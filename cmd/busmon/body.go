package main

import (
	"strings"
)

// Overlay bodies are Lines (busmon Charm spec, step a): role-tagged segments,
// the same model the panels draw, never tview colour tags. The builders do not
// escape anything; linesToTview, the one place a tview tag is written, escapes
// every segment's text once, for the overlay TextView that still shows them.

// body builds an overlay body. A segment's text may hold "\n": each one starts
// a new line, so a body ending in "\n" ends with an empty line, as the tagged
// strings did.
type body struct{ lines []Line }

func (b *body) cur() *Line {
	if len(b.lines) == 0 {
		b.lines = append(b.lines, nil)
	}
	return &b.lines[len(b.lines)-1]
}

// seg appends text in role r.
func (b *body) seg(r Role, s string) *body { return b.segs(Seg{Text: s, Role: r}) }

// text appends text in the body's text role.
func (b *body) text(s string) *body { return b.seg(RoleText, s) }

// segs appends segments (a Line built elsewhere) to the current line.
func (b *body) segs(ss ...Seg) *body {
	for _, s := range ss {
		parts := strings.Split(s.Text, "\n")
		for i, p := range parts {
			if i > 0 {
				b.lines = append(b.lines, nil)
			}
			if p != "" {
				s.Text = p
				l := b.cur()
				*l = append(*l, s)
			} else {
				b.cur()
			}
		}
	}
	return b
}

// nl ends the current line.
func (b *body) nl() *body { b.cur(); b.lines = append(b.lines, nil); return b }

// join appends lines at the current position, as text joined by "\n" would
// be: the first continues the current line, each next one starts a line.
func (b *body) join(ls []Line) *body {
	for i, l := range ls {
		if i > 0 {
			b.nl()
		}
		b.segs(l...)
	}
	return b
}

// at is the index of the current line (the number of line breaks so far).
func (b *body) at() int { return max(0, len(b.lines)-1) }

// trimEnd drops the empty lines at the end, as trimming "\n" off text did.
func trimEnd(ls []Line) []Line {
	for len(ls) > 0 && len(ls[len(ls)-1]) == 0 {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// Lines is the body.
func (b *body) Lines() []Line { return b.lines }

// S is one segment.
func S(r Role, s string) Seg { return Seg{Text: s, Role: r} }

// linesPlain is a body's text, lines joined by "\n": what the person reads.
func linesPlain(ls []Line) string {
	var sb strings.Builder
	for i, l := range ls {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(l.text())
	}
	return sb.String()
}

// linesToTview is the one adapter from Lines to the overlay TextView's tagged
// text: every segment's text is escaped, then coloured by its role (no colour
// with the none palette) and made bold when the segment is.
func linesToTview(ls []Line) string {
	var sb strings.Builder
	for i, l := range ls {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(lineToTview(l))
	}
	return sb.String()
}

func lineToTview(l Line) string {
	var sb strings.Builder
	for _, s := range l {
		sb.WriteString(th.segTag(s))
	}
	return sb.String()
}
