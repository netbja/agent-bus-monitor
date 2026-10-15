package main

import (
	"regexp"
	"strings"
	"testing"
)

var tagRE = regexp.MustCompile(`\[[a-zA-Z0-9#:,\-]*\]`)

func plainMD(s string) []string { return strings.Split(tagRE.ReplaceAllString(s, ""), "\n") }

func TestMarkdownBlocks(t *testing.T) {
	src := "## Decision\nThe act is **approved** at `4d5d51b`\nand the window opens on Monday.\n\n- first item that is long enough to wrap onto a second line here\n  - nested\n1. one\n> a quote\n```\ncode   kept  as is\n```\nend"
	got := plainMD(renderMarkdown(src, 40))
	want := []string{
		"Decision",
		"The act is approved at 4d5d51b and the",
		"window opens on Monday.",
		"",
		GlyphNone + " first item that is long enough to wrap",
		"  onto a second line here",
		"  " + GlyphNone + " nested",
		"1. one",
		glyphVertical + " a quote",
		"  code   kept  as is",
		"end",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\n---\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if Width(l) > 40 {
			t.Fatalf("line over width: %q", l)
		}
	}
}

func TestMarkdownLosesNoWord(t *testing.T) {
	src := "SYNTHESE ACTE DCA n4 + DECISION BERNARD. Etat verifie: acte n4 (docs seul) approuve par foureyes a 4d5d51b; DECISION 1 - date cible T: ma proposition lundi 2026-10-19 vers 10:00 UTC. supercalifragilisticexpialidocious-and-even-longer-word-here"
	got := strings.Join(plainMD(renderMarkdown(src, 30)), " ")
	for _, w := range strings.Fields(src) {
		if !strings.Contains(strings.ReplaceAll(got, " ", ""), strings.ReplaceAll(w, " ", "")) {
			t.Fatalf("word %q lost:\n%s", w, got)
		}
	}
	for _, l := range plainMD(renderMarkdown(src, 30)) {
		if Width(l) > 30 {
			t.Fatalf("line over width: %q", l)
		}
	}
}

func TestMarkdownSanitises(t *testing.T) {
	got := renderMarkdown("hello \x1b[31mred\x1b[0m [yellow]tag", 40)
	if strings.Contains(got, "\x1b") || !strings.Contains(got, "[yellow[]tag") || !strings.Contains(got, "red") {
		t.Fatalf("escape kept or text lost: %q", got)
	}
}
