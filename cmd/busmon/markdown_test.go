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
	plain := strings.Join(plainMD(renderMarkdown(src, 60)), "\n")
	for _, w := range strings.Fields(strings.NewReplacer("**", "").Replace(src)) {
		if !strings.Contains(plain, strings.TrimSuffix(w, ".")) {
			t.Errorf("rendering lost %q", w)
		}
	}
}

func TestActivityEntryHangsAndCaps(t *testing.T) {
	got := strings.Join(plainMD(activityEntry("23:33", "reply", RoleText, "foureyes -> master", strings.Repeat("blocker ", 60), 60)), "\n")
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
