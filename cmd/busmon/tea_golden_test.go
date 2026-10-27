package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// goldenStates are the screens the goldens draw (busmon Charm spec, step b
// tests): the dashboard, the report with a long code block expanded in
// Activity, that report in the reader, and the compose window open.
var goldenStates = []string{"dashboard", "expanded", "reader", "compose"}

// reportID is the fixture's report, whose fenced code is longer than any width.
var reportID = fmt.Sprintf("%d-0", shellNow.Add(-13*time.Minute).UnixMilli())

// inState is the fixture shell brought to a golden state by keys.
func inState(t testing.TB, m Model, state string) Model {
	t.Helper()
	switch state {
	case "dashboard":
		return m
	case "compose": // a directive to coder, two lines written
		m = typeText(t, keys(t, m, 'I'), "@cod")
		m = typeText(t, keys(t, m, kTab), "Review lot 1 please: the reader, the ask mode and the cache.")
		return typeText(t, keys(t, m, kEnter), "Thanks")
	}
	tab := rune('1') // Activity, the tabbed panel's first tab
	if m.layout.Tier == TierNarrow {
		tab = '3' // the narrow tier's Activity view
	}
	m = keys(t, m, tab)
	m.lists.activity.Select(feedKey(t, m, reportID))
	m = keys(t, m, kEnter)
	if state == "reader" {
		m = keys(t, m, kEnter)
	}
	return m
}

// feedKey is the Activity list key of the entry id.
func feedKey(t testing.TB, m Model, id string) string {
	t.Helper()
	for _, fl := range m.feed.feed {
		if fl.ev.ID == id && !fl.sep {
			return fl.id
		}
	}
	t.Fatalf("no feed line for %s", id)
	return ""
}

// The shell's View in each golden state, at each size, dark and light: the
// text (.txt) and the styled content (.ansi), the same under both renderer
// width methods.
func TestShellGoldens(t *testing.T) {
	pinLocal(t)
	for _, state := range goldenStates {
		for _, pal := range []string{"dark", "light"} {
			for _, size := range goldenSizes {
				t.Run(fmt.Sprintf("%s-%s-%dx%d", state, pal, size[0], size[1]), func(t *testing.T) {
					shellGolden(t, state, pal, size)
				})
			}
		}
	}
}

func shellGolden(t *testing.T, state, pal string, size [2]int) {
	withPalette(t, pal, false, 1<<24)
	m := inState(t, fixtureShell(t, size[0], size[1]), state)
	m.paint = newPainter(th)
	content := m.View().Content
	plain := m.grid().plain()
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		if got := placed(content, size[0], size[1], method); got != plain {
			t.Fatalf("width method %v places the View otherwise:\n%s\nwant:\n%s", method, got, plain)
		}
	}
	base := fmt.Sprintf("%s-%dx%d-%s", state, size[0], size[1], pal)
	checkGolden(t, base+".txt", plain+"\n")
	checkGolden(t, base+".ansi", content+"\n")
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

// At the wide tier Agents beside For you keeps room for the message: its
// column header shows "message", and every row has text under it, not a lone
// ellipsis.
func TestShellWideAgentsKeepTheMessage(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{120, 30}, {140, 40}} {
		m := fixtureShell(t, size[0], size[1])
		g := m.grid()
		r := m.layout.Agents
		row := func(y int) []string {
			var cells []string
			for x := r.X + 1; x < r.X+r.W-1; x++ {
				if c := g.at(x, y); !c.cont {
					cells = append(cells, c.text)
				}
			}
			return cells
		}
		head := row(r.Y + 1)
		at := strings.Index(strings.Join(head, ""), "message")
		if at < 0 {
			t.Fatalf("%v: Agents header lacks the message column: %q", size, strings.Join(head, ""))
		}
		for y := r.Y + 2; y < r.Y+r.H-1; y++ {
			msg := strings.TrimSpace(strings.Join(row(y)[at:], ""))
			if len([]rune(msg)) < 3 {
				t.Errorf("%v: Agents row %d has no room for its message: %q", size, y, strings.Join(row(y), ""))
			}
		}
	}
}
