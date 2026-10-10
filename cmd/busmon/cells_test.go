package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
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
	for s, want := range map[string]string{"a\tb\nc\r": "a b c ", "\x1b[31mred": "[31mred", "\u202eabc": "abc", "x\u200dy": "x\u200dy", "é": "é", "\x7f": "", "\u0085": "", "❤️": "❤️", "[red]": "[red]"} {
		if got := Preview(s); got != want {
			t.Errorf("Preview(%q)=%q want %q", s, got, want)
		}
	}
	if got := Preview("a\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069b"); got != "ab" {
		t.Fatal(got)
	}
}

func TestDetailText(t *testing.T) {
	for s, want := range map[string]string{"a\nb": "a\nb", "a\tb": "a   b", "ab\tc": "ab  c", "日\tx": "日  x", "é\tx": "é   x", "👩‍💻\tx": "👩‍💻  x", "abcd\tx": "abcd    x", "ab\n\tx": "ab\n    x", "\u202eabc": "abc", "a\rb": "a b"} {
		if got := DetailText(s); got != want {
			t.Errorf("DetailText(%q)=%q want %q", s, got, want)
		}
	}
}

func cellScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

func TestDrawFrame(t *testing.T) {
	s := cellScreen(t, 20, 6)
	DrawFrame(s, 0, 0, 20, 6, Frame{Title: Line{{Text: strings.Repeat("t", 40), Role: RoleText}}, Label: Line{{Text: "label", Role: RoleMuted}}, Scroll: ScrollState{Total: 10, Top: 0, Visible: 4}})
	for _, tt := range []struct {
		x, y int
		r    rune
	}{{0, 0, '╭'}, {19, 0, '╮'}, {0, 5, '╰'}, {19, 5, '╯'}, {19, 1, '▲'}, {19, 4, '▼'}, {19, 2, '┃'}} {
		r, _, _, _ := s.GetContent(tt.x, tt.y)
		if r != tt.r {
			t.Errorf("(%d,%d)=%q want %q", tt.x, tt.y, r, tt.r)
		}
	}
	// The helper must tolerate tiny frames and keep writes in their rectangle.
	for w := 0; w <= 8; w++ {
		for h := 0; h <= 6; h++ {
			s.Fill('.', tcell.StyleDefault)
			DrawFrame(s, 2, 0, w, h, Frame{Title: Line{{Text: "日本title", Role: RoleText}}, Scroll: ScrollState{Total: 100, Top: 99, Visible: 1}})
			for y := 0; y < 6; y++ {
				for x := 0; x < 20; x++ {
					if x < 2 || x >= 2+w || y >= h {
						r, _, _, _ := s.GetContent(x, y)
						if r != '.' {
							t.Fatalf("write outside %dx%d at %d,%d", w, h, x, y)
						}
					}
				}
			}
		}
	}
}

func TestDrawLineStylesAndBounds(t *testing.T) {
	s := cellScreen(t, 12, 2)
	s.Fill('.', tcell.StyleDefault)
	DrawLine(s, 2, 0, 5, Line{{Text: "日", Role: RoleError, Bold: true}, {Text: "[x]", Role: RoleText}}, true)
	r, _, style, _ := s.GetContent(2, 0)
	if r != '日' || style != th.Selected(RoleError).Bold(true) {
		t.Fatalf("first cell %q %v", r, style)
	}
	for _, x := range []int{0, 1, 7, 8, 9, 10, 11} {
		r, _, _, _ := s.GetContent(x, 0)
		if r != '.' {
			t.Fatalf("outside draw at %d", x)
		}
	}
	DrawLine(s, 0, 1, 3, Line{{Text: "éx", Role: RoleOK}}, false)
	r, c, _, _ := s.GetContent(0, 1)
	if r != 'e' || string(c) != "́" {
		t.Fatal("combining cluster lost", r, c)
	}
}
