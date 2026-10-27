package main

import (
	"regexp"
	"testing"
)

// drawnText is what the person reads of a body in the overlay: its Lines
// through the one adapter, read back by a tview TextView with tags on.
// drawnText is a body as the screen shows it: a tag written in a message is
// text (the shell writes no markup), so it shows as written.
func drawnText(ls []Line) string { return linesPlain(ls) }

func TestBodyBuilderKeepsTextSemantics(t *testing.T) {
	var b body
	b.text("a").seg(RoleMuted, "b\nc").text("\n\n").join([]Line{{S(RoleOK, "d")}, {S(RoleOK, "e")}}).text("\n")
	if got := linesPlain(b.Lines()); got != "ab\nc\n\nd\ne\n" {
		t.Fatalf("plain = %q", got)
	}
	if b.at() != 5 {
		t.Fatalf("at = %d", b.at())
	}
	if got := linesPlain(trimEnd(b.Lines())); got != "ab\nc\n\nd\ne" {
		t.Fatalf("trimEnd = %q", got)
	}
}

// hostile is agent text that looks like every kind of tview tag.
const hostile = `[red]a[::b]b["x"]c[-]d`

// tviewTag is tview's colour/style tag syntax, the names this package used.
var tviewTag = regexp.MustCompile(`\[(-|::[a-z-]*|#[0-9a-fA-F]{6}|(red|gray|grey|yellow|aqua|white|green|blue|fuchsia|silver|maroon|olive|teal|purple|orange|cyan)(::[a-z-]*)?)\]`)
