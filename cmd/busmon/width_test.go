package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Three width methods measure what busmon prints: its own (uniseg, Width),
// lipgloss's (ansi.StringWidth, grapheme clusters) and bubbletea's renderer
// on a terminal without Unicode mode 2027 (ansi.StringWidthWc). Text they
// disagree on would be measured one way and placed another (busmon Charm
// spec, "Width: one method, enforced").
func widths(s string) (int, int, int) {
	return uniseg.StringWidth(s), ansi.StringWidth(s), ansi.StringWidthWc(s)
}

// hostileWidths are clusters the methods are known to read differently, and
// plain ones they agree on.
var hostileWidths = []string{
	"plain ascii", "日本語", "é", "é", "👩‍💻", "❤️", "❤", "🇫🇷", "☀️", "1️⃣",
	"x‍y", "👍🏽", "ǟ", "中文 and 👩‍👩‍👧 mixed", "❤︎", "🏳️‍🌈",
}

func TestWidthMethodsAgree(t *testing.T) {
	for _, h := range hostileWidths {
		for _, got := range append([]string{Preview(h)}, strings.Split(DetailText(h+"\n"+h), "\n")...) {
			if u, g, w := widths(got); u != g || u != w {
				t.Errorf("%q sanitised to %q: uniseg %d, grapheme %d, wcwidth %d", h, got, u, g, w)
			}
		}
	}
	// Text the methods already agree on is left as written.
	for _, ok := range []string{"plain ascii", "日本語", "é", "é"} {
		if Preview(ok) != ok || DetailText(ok) != ok {
			t.Errorf("%q was changed: %q / %q", ok, Preview(ok), DetailText(ok))
		}
	}
}

func TestGlyphSetIsOneCell(t *testing.T) {
	for _, g := range append(append([]string{}, UIGlyphs...), widthReplacement) {
		if u, gr, w := widths(g); u != 1 || gr != 1 || w != 1 {
			t.Errorf("glyph %q: uniseg %d, grapheme %d, wcwidth %d", g, u, gr, w)
		}
	}
}
