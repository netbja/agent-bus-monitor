package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestGlyphsAreSingleCell(t *testing.T) {
	if len(UIGlyphs) != 26 {
		t.Fatal(len(UIGlyphs))
	}
	seen := map[string]bool{}
	for _, g := range UIGlyphs {
		if Width(g) != 1 || seen[g] {
			t.Errorf("invalid glyph %q", g)
		}
		seen[g] = true
	}
}

// Every literal busmon draws may hold ASCII and the closed UI glyph set only
// (spec §5.1): a glyph outside it could be two cells wide, or turn into an
// emoji, and push a frame's right border out of its column.
func TestNoStrayNonASCIILiterals(t *testing.T) {
	allowed := map[rune]bool{}
	for _, g := range UIGlyphs {
		for _, r := range g {
			allowed[r] = true
		}
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if p == "theme.go" || strings.HasSuffix(p, "_test.go") {
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Error(err)
				return true
			}
			for _, r := range s {
				if r > 127 && !allowed[r] {
					t.Errorf("%s: %q is outside the UI glyph set", fs.Position(lit.Pos()), r)
					break
				}
			}
			return true
		})
	}
}

func TestResolveTheme(t *testing.T) {
	for _, tt := range []struct {
		flag, env, bg string
		none          bool
		colors        int
		want          Palette
	}{
		{"light", "", "", false, 1 << 24, PaletteLight}, {"", "dark", "", false, 1 << 24, PaletteDark},
		{"light", "dark", "", false, 1 << 24, PaletteLight}, {"auto", "light", "0;15", false, 1 << 24, PaletteLight},
		{"auto", "", "15;0", false, 1 << 24, PaletteDark}, {"", "", "", false, 1 << 24, PaletteDark},
		{"auto", "", "0;7", false, 1 << 24, PaletteLight}, {"light", "", "", true, 1 << 24, PaletteNone},
		{"dark", "", "", false, 16, Palette16}, {"light", "", "", false, 256, Palette16},
	} {
		got, err := ResolveTheme(tt.flag, tt.env, tt.bg, tt.none, tt.colors)
		if err != nil || got.Palette() != tt.want {
			t.Errorf("%+v: %v %v", tt, got, err)
		}
	}
	for _, v := range []string{"blue", "DARK", " "} {
		if _, err := ResolveTheme(v, "", "", true, 16); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
}

func TestStyleRoles(t *testing.T) {
	dark, _ := ResolveTheme("dark", "", "", false, 1<<24)
	light, _ := ResolveTheme("light", "", "", false, 1<<24)
	none, _ := ResolveTheme("dark", "", "", true, 1<<24)
	fg, bg, _ := dark.Style(RoleOK).Decompose()
	if fg != tcell.GetColor("#7EE2A8") || bg != tcell.ColorDefault {
		t.Fatal(fg, bg)
	}
	fg, _, _ = light.Style(RoleError).Decompose()
	if fg != tcell.GetColor("#CF222E") {
		t.Fatal(fg)
	}
	fg, bg, _ = none.Style(RoleError).Decompose()
	if fg != tcell.ColorDefault || bg != tcell.ColorDefault {
		t.Fatal(fg, bg)
	}
	if got := dark.segTag(S(RoleOK, "[x]")); got != "[#7ee2a8][x[][-]" {
		t.Fatal(got)
	}
	if got := none.segTag(S(RoleOK, "[x]")); got != "[x[]" {
		t.Fatal(got)
	}
	if got := none.segTag(Seg{Text: "b", Role: RoleOK, Bold: true}); got != "[::b]b[::-]" {
		t.Fatal(got) // bold stays without colours, as the markdown's [::b] did
	}
	for thm, want := range map[*Theme]string{dark: "#22324A", light: "#DDF4FF"} {
		if _, bg, _ := thm.Selected(RoleText).Decompose(); bg != tcell.GetColor(want) {
			t.Fatal("selection background", bg, want)
		}
	}
	c16, _ := ResolveTheme("dark", "", "", false, 16)
	if got := c16.segTag(S(RoleText, "x")); got != "x" {
		t.Fatal(got)
	}
	for _, colors := range []int{16, 1 << 24} {
		for _, n := range []bool{false, true} {
			thm, _ := ResolveTheme("dark", "", "", n, colors)
			_, bg, attrs := thm.Selected(RoleText).Decompose()
			if n || colors == 16 {
				if attrs&tcell.AttrReverse == 0 || bg != tcell.ColorDefault {
					t.Fatal("missing reverse", bg, attrs)
				}
			}
			if n {
				_, _, attrs = thm.Border(true).Decompose()
				if attrs&tcell.AttrBold == 0 {
					t.Fatal("focused border not bold")
				}
			}
		}
	}
}

func TestStyleInputAutocomplete(t *testing.T) {
	for _, name := range []string{"light", "dark"} {
		for _, noColor := range []bool{false, true} {
			thm, _ := ResolveTheme(name, "", "", noColor, 1<<24)
			f := tview.NewInputField().SetLabel("> ")
			thm.StyleInput(f)
			if f.GetFieldStyle() != thm.Style(RoleText) || f.GetLabelStyle() != thm.Style(RoleAccent) || f.GetPlaceholderStyle() != thm.Style(RoleMuted) {
				t.Fatal("input styles not applied")
			}
			f.SetAutocompleteFunc(func(string) []string { return []string{"alpha", "beta"} })
			f.SetText("a")
			f.Autocomplete()
			f.SetRect(0, 0, 20, 1)
			f.Focus(func(tview.Primitive) {})
			s := cellScreen(t, 20, 5)
			f.Draw(s)
			// Inspect actual rendered menu cells, not a getter duplicating the setter.
			foundMain, foundSelected := false, false
			for y := 1; y < 5; y++ {
				for x := 0; x < 20; x++ {
					r, _, sty, _ := s.GetContent(x, y)
					if r == 'a' && sty == thm.Selected(RoleText) {
						foundSelected = true
					}
					if r == 'b' && sty == thm.Style(RoleText) {
						foundMain = true
					}
				}
			}
			if !foundMain || !foundSelected {
				t.Fatalf("%s noColor=%v: menu main=%v selected=%v", name, noColor, foundMain, foundSelected)
			}
		}
	}
}

func TestStyleTextView(t *testing.T) {
	thm, _ := ResolveTheme("light", "", "", false, 1<<24)
	v := tview.NewTextView()
	thm.StyleTextView(v)
	v.SetText("text")
	v.SetRect(0, 0, 8, 2)
	s := cellScreen(t, 8, 2)
	v.Draw(s)
	_, _, sty, _ := s.GetContent(0, 0)
	if sty != thm.Style(RoleText) || v.GetBackgroundColor() != tcell.ColorDefault {
		t.Fatal(sty)
	}
}
