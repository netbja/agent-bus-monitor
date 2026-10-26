package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var updateGoldens = flag.Bool("update", false, "rewrite testdata/golden/*")

// goldenSizes: the narrow, medium and wide tiers (busmon Charm spec, step b),
// and a tall wide screen where every agent row, so every flag, is on screen.
var goldenSizes = [][2]int{{60, 20}, {80, 24}, {140, 40}, {140, 60}}

// withPalette runs f with the theme of that palette and restores it.
func withPalette(t testing.TB, name string, noColor bool, colors int) {
	t.Helper()
	old := th
	nt, err := ResolveTheme(name, "", "", noColor, colors)
	if err != nil {
		t.Fatal(err)
	}
	th = nt
	t.Cleanup(func() { th = old })
}

// checkGolden compares got with testdata/golden/name byte for byte, or
// rewrites it with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run go test -run TestShellGoldens -update)", name, err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// The dashboard's View, at each size, dark and light: the text (.txt) and the
// styled content (.ansi), the same under both renderer width methods.
func TestShellGoldens(t *testing.T) {
	pinLocal(t)
	for _, pal := range []string{"dark", "light"} {
		for _, size := range goldenSizes {
			t.Run(fmt.Sprintf("%s-%dx%d", pal, size[0], size[1]), func(t *testing.T) {
				withPalette(t, pal, false, 1<<24)
				m := fixtureShell(t, size[0], size[1])
				m.paint = newPainter(th)
				content := m.View().Content
				plain := m.grid().plain()
				for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
					if got := placed(content, size[0], size[1], method); got != plain {
						t.Fatalf("width method %v places the View otherwise:\n%s\nwant:\n%s", method, got, plain)
					}
				}
				base := fmt.Sprintf("dashboard-%dx%d-%s", size[0], size[1], pal)
				checkGolden(t, base+".txt", plain+"\n")
				checkGolden(t, base+".ansi", content+"\n")
			})
		}
	}
}

// Every surviving agent flag, state look and header item is on a golden
// (busmon Charm spec, "Flags and header items that survive unchanged").
func TestShellGoldensCarryEverySurvivingFlag(t *testing.T) {
	pinLocal(t)
	got := fixtureShell(t, 140, 60).grid().plain()
	for _, w := range []string{
		// agent row flags and state looks
		"busy", "prompt", "update", "model" + GlyphNotEqual, "swapping", "fallback kimi-alt", "held", "quota",
		"gate 1", "for you", "deferred", "escalating", "escalated", "paused", "pilot", "armed", "stale 2",
		"1 dormant (a shows)",
		// header items
		"Bernard [human]", "agent-bus-monitor", "bus ok", "dispatch up", "missions up", "resumes in 1h52",
		"grok 5h", "100% held", "claude 5h", "2 unread", GlyphHuman + " 4 for you", "15:04:05",
		// Activity marks
		GlyphDotFull + " 14:56", GlyphIdle + " 14:52", "(+197)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("no %q on the 140x60 dashboard:\n%s", w, got)
		}
	}
}
