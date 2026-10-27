package main

import (
	"strings"
	"testing"
)

func lineText(l Line) string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestWidthClusters(t *testing.T) {
	for s, want := range map[string]int{"abc": 3, "日本": 4, "é": 1, "👩‍💻": 2, "❤️": 2, "": 0} {
		if got := Width(s); got != want {
			t.Errorf("Width(%q)=%d, want %d", s, got, want)
		}
	}
}

func TestTruncateFit(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatal(got)
	}
	for _, tt := range []struct {
		s    string
		w    int
		want string
	}{
		{"ab", 4, "ab  "}, {"日本語", 5, "日本…"}, {"日本語", 4, "日… "},
		{"éxyz", 2, "é…"}, {"👩‍💻ab", 3, "👩‍💻…"}, {"x", 0, ""}, {"x", -1, ""},
	} {
		if got := Fit(tt.s, tt.w); got != tt.want {
			t.Errorf("Fit(%q,%d)=%q want %q", tt.s, tt.w, got, tt.want)
		}
	}
	for _, s := range []string{"", "abcdef", "日本語", "é", "👩‍💻x❤️", "[red]x"} {
		for w := 1; w <= 12; w++ {
			if got := Width(Fit(s, w)); got != w {
				t.Errorf("Fit(%q,%d) width %d", s, w, got)
			}
		}
	}
}

func TestFitLineWideClusterAtEdge(t *testing.T) {
	l := Line{{Text: "ab", Role: RoleText}, {Text: "日本", Role: RoleMuted, Bold: true}}
	for w := 1; w <= 8; w++ {
		got := FitLine(l, w)
		if got.Width() != w {
			t.Fatalf("width %d: %#v", w, got)
		}
		if w < l.Width() && !strings.HasSuffix(strings.TrimRight(lineText(got), " "), "…") {
			t.Errorf("missing ellipsis: %#v", got)
		}
	}
	got := FitLine(l, 4)
	if lineText(got) != "ab… " {
		t.Fatal(got)
	}
	for _, s := range got {
		if strings.Contains(s.Text, "…") && (s.Role != RoleMuted || !s.Bold) {
			t.Fatal("cut segment style lost", got)
		}
	}
	// Segment boundaries must not split a grapheme or mutate the input.
	split := Line{{Text: "e", Role: RoleOK}, {Text: "́x", Role: RoleError}}
	if split.Width() != 2 || lineText(FitLine(split, 2)) != "éx" {
		t.Fatal("split cluster lost")
	}
	if l[1].Text != "日本" {
		t.Fatal("input mutated")
	}
}

func TestPreview(t *testing.T) {
	for s, want := range map[string]string{"a\tb\nc\r": "a b c ", "\x1b[31mred": "[31mred", "\u202eabc": "abc", "x\u200dy": "x\u200dy", "é": "é", "\x7f": "", "\u0085": "", "❤️": "❤" /* one width under every method: agreeWidths */, "[red]": "[red]"} {
		if got := Preview(s); got != want {
			t.Errorf("Preview(%q)=%q want %q", s, got, want)
		}
	}
	if got := Preview("a\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069b"); got != "ab" {
		t.Fatal(got)
	}
}

func TestDetailText(t *testing.T) {
	for s, want := range map[string]string{"a\nb": "a\nb", "a\tb": "a   b", "ab\tc": "ab  c", "日\tx": "日  x", "é\tx": "é   x", "👩‍💻\tx": "👩  x" /* the ZWJ sequence keeps its first rune */, "abcd\tx": "abcd    x", "ab\n\tx": "ab\n    x", "\u202eabc": "abc", "a\rb": "a b"} {
		if got := DetailText(s); got != want {
			t.Errorf("DetailText(%q)=%q want %q", s, got, want)
		}
	}
}

// A frame title keeps its width whatever the focus (tab click spans are
// computed from it); focused, its name is a pill that takes the spaces around
// it; muted tabs and status words keep their roles.
func TestFrameTitleHueAndPill(t *testing.T) {
	cases := []Line{
		{{Text: "Agents", Role: RoleText}},
		{{Text: "Activity", Role: RoleText}, {Text: "  auto-scroll on", Role: RoleOK}},
		{{Text: " 1 Activity ", Role: RoleOK, Bold: true}, {Text: " 2 Board ", Role: RoleMuted}, {Text: " 3 Requests ", Role: RoleMuted}},
		{{Text: " 1 Activity ", Role: RoleMuted}, {Text: " 2 Board ", Role: RoleDispatch, Bold: true}},
	}
	for _, c := range cases {
		want := c.Width() + 2
		for _, focused := range []bool{false, true} {
			got := frameTitle(c, RoleOK, focused)
			if got.Width() != want {
				t.Errorf("%+v focused=%v: width %d, want %d", c, focused, got.Width(), want)
			}
			for _, s := range got {
				if s.Role == RoleText && strings.TrimSpace(s.Text) != "" {
					t.Errorf("%+v focused=%v: a name kept RoleText: %+v", c, focused, s)
				}
				if !focused && s.Pill {
					t.Errorf("unfocused title has a pill: %+v", s)
				}
				if s.Role == RoleMuted && s.Pill {
					t.Errorf("an inactive tab became a pill: %+v", s)
				}
			}
		}
	}
	got := frameTitle(Line{{Text: "Agents", Role: RoleText}}, RoleAccent, true)
	if len(got) != 1 || got[0].Text != " Agents " || !got[0].Pill || got[0].Role != RoleAccent {
		t.Errorf("focused simple title = %+v, want one pill \" Agents \"", got)
	}
	got = frameTitle(Line{{Text: "Activity", Role: RoleText}, {Text: "  auto-scroll on", Role: RoleOK}}, RoleOK, true)
	if got[0].Text != " Activity " || !got[0].Pill || got[1].Text != " auto-scroll on" || got[1].Pill {
		t.Errorf("focused Activity title = %+v", got)
	}
}

// The padding after a line never inherits a reversed (cursor) cell: the
// compose window's cursor at the end of a row lit the whole row (A1).
func TestFitLinePaddingIsNotReversed(t *testing.T) {
	l := Line{{Text: "essai", Role: RoleText}, {Text: " ", Role: RoleText, Reverse: true}}
	got := FitLine(l, 12)
	if got.Width() != 12 {
		t.Fatalf("width %d", got.Width())
	}
	reversed := 0
	for _, s := range got {
		if s.Reverse {
			reversed += Line{s}.Width()
		}
	}
	if reversed != 1 {
		t.Fatalf("%d reversed cells, want only the cursor: %+v", reversed, got)
	}
}
