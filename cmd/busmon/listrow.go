package main

import (
	"fmt"
	"strings"
)

// The one list pattern of busmon's pages (spec
// docs/superpowers/specs/2026-10-31-busmon-pages-mockups.md, "The pattern"):
// one line per item (symbol, name, short state, age at the right edge), a
// counter summary in the title, sections in the order the person acts in, and
// the quiet items folded into one last line a key shows. Ids, timestamps and
// remediation live only in the expanded detail.

// listRow is one item of a list page.
type listRow struct {
	Sym   string // from the glyph set, or "?" for not measured
	Role  Role   // the symbol's
	Name  string
	State string
	Note  Line   // an optional column before the age: a command, who deals with it
	Age   string // humanAge, or ""
}

// listAgeW is the age column: "59s", "23h", "120d".
const listAgeW = 4

// line lays the row out in width cells: the selection mark, the symbol, the
// name in nameW, the state in what is left, the note in noteW (0: none) and
// the age right-aligned. A note that would leave the state under 8 cells is
// dropped, never the state.
func (r listRow) line(selected bool, width, nameW, noteW int) Line {
	fixed := 2 + Width(r.Sym) + 1 + nameW + 2 + 2 + listAgeW
	if noteW > 0 && width-fixed-noteW-2 < 8 {
		noteW = 0
	}
	stateW := width - fixed
	if noteW > 0 {
		stateW -= noteW + 2
	}
	l := append(Line{}, planMark(selected)...)
	l = append(l, S(r.Role, r.Sym), S(RoleText, " "+Fit(Preview(r.Name), nameW)+"  "))
	l = append(l, S(RoleText, Fit(Preview(r.State), max(0, stateW))))
	if noteW > 0 {
		note := FitLine(r.Note, noteW)
		l = append(append(l, S(RoleText, "  ")), note...)
		l = append(l, S(RoleText, strings.Repeat(" ", max(0, noteW-note.Width()))))
	}
	age := Truncate(r.Age, listAgeW)
	return append(l, S(RoleText, "  "+strings.Repeat(" ", listAgeW-Width(age))), S(RoleMuted, age))
}

// listCount is one part of a title's counter summary.
type listCount struct {
	N    int
	Word string
}

// listSummary is the title's counter summary, "2 to do · 11 fine": the parts
// with a count, in order, in the words of the page's sections.
func listSummary(parts ...listCount) string {
	var out []string
	for _, p := range parts {
		if p.N > 0 {
			out = append(out, fmt.Sprintf("%d %s", p.N, p.Word))
		}
	}
	return strings.Join(out, " "+GlyphNone+" ")
}

// listSection is a section's heading.
func listSection(name string) Line {
	return Line{S(RoleText, "  "), {Text: name, Role: RoleText, Bold: true}}
}

// listFold is the last line of a list: the n quiet items folded, and the key
// that shows them ("── fine · 11  (a shows them) ──").
func listFold(what string, n int, keys string) Line {
	h := strings.Repeat(glyphHorizontal, 2)
	return Line{S(RoleMuted, fmt.Sprintf("  %s %s %s %d  (%s shows them) %s", h, what, GlyphNone, n, keys, h))}
}
