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
	got := plainMD(plainOf(renderMarkdown(src, 40)))
	want := []string{
		"Decision",
		"The act is approved at 4d5d51b",
		"and the window opens on Monday.",
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
	got := strings.Join(plainMD(plainOf(renderMarkdown(src, 30))), " ")
	for _, w := range strings.Fields(src) {
		if !strings.Contains(strings.ReplaceAll(got, " ", ""), strings.ReplaceAll(w, " ", "")) {
			t.Fatalf("word %q lost:\n%s", w, got)
		}
	}
	for _, l := range plainMD(plainOf(renderMarkdown(src, 30))) {
		if Width(l) > 30 {
			t.Fatalf("line over width: %q", l)
		}
	}
}

func TestMarkdownSanitises(t *testing.T) {
	got := renderMarkdown("hello \x1b[31mred\x1b[0m [yellow]tag", 40)
	if strings.Contains(plainOf(got), "\x1b") || !strings.Contains(drawnText(got), "[yellow]tag") || !strings.Contains(plainOf(got), "red") {
		t.Fatalf("escape kept or text lost: %q", plainOf(got))
	}
}

func TestStructureFlatSplitsLabelsAndChoices(t *testing.T) {
	src := "SYNTHESE ACTE n4. Etat verifie: acte approuve par foureyes et le texte fige est intact, deploy a atteste en lecture seule. " +
		"CE QUE FAIT L'OUVERTURE: arret propre, archive du log verifiee, nouvelle session en base. " +
		"DECISION 1 - date cible T: lundi 19/10. DECISION 2 - code a figer. Deux choix: (X) figer un SHA de dev avec PR#91, " +
		"(Y) rester sur 82c216c. VERIFIED: exact preservation."
	got := structureFlat(src)
	for _, want := range []string{"\n\n**CE QUE FAIT L'OUVERTURE:** arret", "\n\n**DECISION 1 -** date", "\n\n**DECISION 2 -** code", "\n- (X) figer", "\n- (Y) rester", "\n\n**VERIFIED:** exact"} {
		if !strings.Contains(got, want) {
			t.Errorf("structureFlat lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "**SYNTHESE ACTE n4.**\n\nEtat") || strings.Contains(got, "(acte") {
		t.Errorf("structureFlat missed the title or split a parenthesis:\n%s", got)
	}
	for _, keep := range []string{"short. DECISION 1 - x", "line one\nDECISION 1 - " + strings.Repeat("x ", 200)} {
		if structureFlat(keep) != keep {
			t.Errorf("structureFlat changed a short or multi-line text: %q", keep)
		}
	}
	one := strings.Repeat("word ", 60) + "only (1) one choice here."
	if structureFlat(one) != one {
		t.Errorf("a single enumerated item became a list")
	}
	plain := strings.Join(plainMD(plainOf(renderMarkdown(src, 60))), "\n")
	for _, w := range strings.Fields(strings.NewReplacer("**", "").Replace(src)) {
		if !strings.Contains(plain, strings.TrimSuffix(w, ".")) {
			t.Errorf("rendering lost %q", w)
		}
	}
}

func TestActivityEntryHangsAndCaps(t *testing.T) {
	got := strings.Join(plainMD(plainOf(activityEntry("23:33", "reply", RoleText, "foureyes -> master", strings.Repeat("blocker ", 60), 60))), "\n")
	lines := strings.Split(got, "\n")
	if len(lines) != recentLines {
		t.Fatalf("want %d lines, got %d:\n%s", recentLines, len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "23:33 reply") || !strings.HasPrefix(lines[1], strings.Repeat(" ", 16)+" ") && !strings.HasPrefix(lines[1], strings.Repeat(" ", 16)) {
		t.Errorf("no hanging indent:\n%s", got)
	}
	for _, l := range lines {
		if Width(l) > 60 {
			t.Errorf("line wider than 60: %q", l)
		}
	}
	if !strings.HasSuffix(lines[2], GlyphEllipsis) {
		t.Errorf("a cut entry must end with an ellipsis: %q", lines[2])
	}
}

// A line break is kept as typed, as GitHub shows a comment: a message is not
// a hard-wrapped document, and "Status:\nline one\nline two" is three lines.
func TestMarkdownKeepsLineBreaks(t *testing.T) {
	got := plainMD(plainOf(renderMarkdown("Status:\nline one\nline two\n\n1) first\n2) second", 80)))
	want := []string{"Status:", "line one", "line two", "", "1. first", "2. second"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A long line still wraps at the width, under itself.
	long := plainMD(plainOf(renderMarkdown(strings.Repeat("word ", 30)+"\nnext", 40)))
	if len(long) < 4 || long[len(long)-1] != "next" {
		t.Fatalf("a long line does not wrap, or swallowed the next one: %q", long)
	}
}

// A fenced code line longer than the width wraps under its gutter, marked as
// continued, and is never cut (busmon Charm spec, step a: the one visible
// change of the step).
func TestMarkdownWrapsCodeNeverCuts(t *testing.T) {
	code := "func veryLongFunctionName(argumentOne string, argumentTwo int) (result 日本語の結果, err error) {"
	got := renderMarkdown("```go\n"+code+"\n```", 30)
	if len(got) < 3 {
		t.Fatalf("not wrapped: %q", plainOf(got))
	}
	var joined strings.Builder
	for i, l := range got {
		text := l.text()
		if w := Width(text); w > 30 {
			t.Errorf("line %d is %d cells: %q", i, w, text)
		}
		if strings.Contains(text, GlyphEllipsis) {
			t.Errorf("line %d is cut: %q", i, text)
		}
		gutter := "  "
		if i > 0 {
			gutter = GlyphArrowR + " "
		}
		if !strings.HasPrefix(text, gutter) {
			t.Errorf("line %d gutter: %q", i, text)
		}
		joined.WriteString(strings.TrimPrefix(text, gutter))
	}
	if joined.String() != code {
		t.Fatalf("the code changed:\n%q\n%q", joined.String(), code)
	}
	for _, l := range got {
		for _, s := range l {
			if s.Role != RoleMuted {
				t.Fatalf("code is muted, its mark too: %+v", l)
			}
		}
	}
	// Spaces that fall on a wrap point are code too, and an empty line stays empty.
	spaced := "abcdefgh    ijklmnop"
	var back strings.Builder
	for i, l := range renderMarkdown("```\n"+spaced+"\n\nx\n```", 12) {
		if i == 2 {
			if l.text() != "" {
				t.Fatalf("empty code line: %q", l.text())
			}
			break
		}
		back.WriteString(strings.TrimPrefix(strings.TrimPrefix(l.text(), "  "), GlyphArrowR+" "))
	}
	if back.String() != spaced {
		t.Fatalf("spaces at the wrap point lost: %q", back.String())
	}
	// A short code line keeps its indentation and is not marked.
	if got := plainOf(renderMarkdown("```\n    x := 1\n```", 30)); got != "      x := 1" {
		t.Fatalf("short line: %q", got)
	}
}
