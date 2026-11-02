package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/netbja/agent-bus-monitor/bus"
)

// shellAlignSizes: the step-b sizes, then the tier edges of the alignment
// suite.
var shellAlignSizes = append([][2]int{{60, 20}, {80, 24}, {120, 30}, {140, 40}}, alignSizes...)

// hostileShell is the shell on the alignment suite's hostile model and feed.
func hostileShell(t testing.TB, w, h int, down bool) Model {
	t.Helper()
	st := hostileShared(shellNow, down)
	st.person = "charlie"
	m := newShell(shellConfig{project: "demo-" + hostileText, self: "charlie"})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	var events []bus.Event
	for i := 0; i < 40; i++ {
		target, forWho := "a02", ""
		switch i % 8 {
		case 3:
			target = "charlie"
		case 2:
			forWho = bus.ForPerson
		}
		events = append(events, bus.Event{ID: fmt.Sprintf("17916494%05d-0", i), Kind: []string{"status", "report", "notify", "cmd"}[i%4],
			Agent: "a00", From: "a01" + strings.Repeat("y", 30), Target: target, Type: "directive", State: "working",
			Message: hostileText, Full: hostileText + hostileText, Ref: []string{"", hostileText}[i%2], For: forWho})
	}
	m = step(t, m, tailMsg{events: events})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}

// The shell's frames stay straight at every size, palette and hostile input,
// on the cell grid its View produces, and that grid is what the terminal
// shows under both width methods.
// midSlide is every gauge of budgets 200 ms into a slide from half its value.
func midSlide(budgets map[string]bus.BudgetSnapshot, now time.Time) map[string]barSlide {
	out := map[string]barSlide{}
	for k, v := range barTargets(budgets) {
		out[k] = barSlide{from: v / 2, to: v, at: now.Add(-200 * time.Millisecond)}
	}
	return out
}

func TestShellAlignmentSuite(t *testing.T) {
	pinLocal(t)
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
	}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, icons := range []string{iconsOff, iconsNerd, iconsFont} { // busmon-icons §5: every mode
			for _, down := range []bool{false, true} {
				for _, sz := range shellAlignSizes {
					for _, tab := range lowerTabs {
						views := []PanelID{PanelForYou}
						if sz[0] < mediumMinWidth {
							views = narrowPanels
						}
						for _, view := range views {
							name := fmt.Sprintf("%s-%d/%s/down=%v/%dx%d/tab %d/view %d", pal.name, pal.colors, icons, down, sz[0], sz[1], tab, view)
							m := hostileShell(t, sz[0], sz[1], down)
							m.ui.tab, m.ui.view = tab, view
							m.cfg.settings.Icons = icons
							withIconTeam(m.snap.agents)
							m.paint = newPainter(th)
							m.bars = midSlide(m.snap.budgets, m.now) // every gauge drawn mid-slide, its leading cell tinted
							m.refresh()
							g := m.grid()
							l := m.layout
							if l.Tier == TierTooSmall {
								continue
							}
							at := func(x, y int) string { return g.at(x, y).text }
							for _, p := range l.Visible {
								checkFrameAt(t, at, name+" panel "+fmt.Sprint(p), l.rect(p), true)
							}
							checkIconPairs(t, name, g, l)
							plain := g.plain()
							if strings.ContainsAny(plain, "\x1b\t\u202e") {
								t.Errorf("%s: control characters reached the screen", name)
							}
							content := m.View().Content
							for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
								if got := placed(content, sz[0], sz[1], method); got != plain {
									t.Errorf("%s: width method %v places it otherwise:\n%s\nwant:\n%s", name, method, got, plain)
								}
							}
							if t.Failed() {
								t.Logf("%s:\n%s", name, plain)
								return
							}
						}
					}
				}
			}
		}
	}
}

// withIconTeam gives every agent a client and provider in turn from the icon
// team, so the icon modes draw icons and blanks among hostile rows.
func withIconTeam(agents map[string]*agentState) {
	var team [][2]string
	for _, n := range []string{"master", "foureyes", "ui-grok", "ui-kimi", "docs"} {
		team = append(team, iconTeam[n])
	}
	team = append(team, [2]string{"", "someone"}) // no icon: two blank cells
	names := make([]string, 0, len(agents))
	for n := range agents {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		agents[n].client, agents[n].provider = team[i%len(team)][0], team[i%len(team)][1]
	}
}

// checkIconPairs: every icon on the screen is followed by its blank cell, and
// that pair lies inside one panel's interior, never on or against a border
// (busmon-icons §4).
func checkIconPairs(t *testing.T, name string, g *grid, l shellLayout) {
	t.Helper()
	icon := map[string]bool{}
	for _, c := range IconGlyphs {
		icon[c] = true
	}
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			if !icon[g.at(x, y).text] {
				continue
			}
			if x+1 >= g.w || g.at(x+1, y).text != " " {
				t.Errorf("%s: the icon at %d,%d is not followed by its blank cell", name, x, y)
				continue
			}
			for _, p := range l.Visible {
				if r := l.rect(p); r.contains(x, y) && (x <= r.X || x+1 >= r.X+r.W-1) {
					t.Errorf("%s: the icon pair at %d,%d touches panel %v's border", name, x, y, p)
				}
			}
		}
	}
}

// Reading and writing keep the frames straight too: a hostile line expanded
// in place, then open in the reader, then hostile text pasted in the compose
// window, at every size and palette.
func TestShellAlignmentReading(t *testing.T) {
	pinLocal(t)
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
	}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, sz := range shellAlignSizes {
			m := hostileShell(t, sz[0], sz[1], false)
			if m.layout.Tier == TierTooSmall {
				continue
			}
			m.paint = newPainter(th)
			m.focusPanel(PanelActivity)
			m.refresh()
			m = keys(t, m, kUp, kUp, kEnter) // a line with a text, expanded
			for _, state := range []string{"expanded", "reader", "compose"} {
				switch state {
				case "reader":
					m = keys(t, m, kEnter)
				case "compose":
					m = keys(t, m, kEsc)
					m = step(t, keys(t, m, 'I', kTab), tea.PasteMsg{Content: hostileText + "\n" + hostileText})
				}
				name := fmt.Sprintf("%s-%d/%dx%d/%s", pal.name, pal.colors, sz[0], sz[1], state)
				if state != "compose" && (state == "expanded") != (m.ui.expanded != "" && !m.rd.open()) {
					t.Fatalf("%s: expanded %q, reader %v", name, m.ui.expanded, m.rd.open())
				}
				g := m.grid()
				at := func(x, y int) string { return g.at(x, y).text }
				for _, p := range m.layout.Visible {
					if m.ui.compose != nil || m.rd.open() && (p == m.layout.Active || m.layout.Tier == TierNarrow) {
						continue // under the window or the reader
					}
					checkFrameAt(t, at, name+" panel "+fmt.Sprint(p), m.layout.rect(p), true)
				}
				if m.rd.open() && m.ui.compose == nil {
					checkFrameAt(t, at, name+" reader", m.readerRect(), true)
				}
				if m.ui.compose != nil {
					f, _ := m.composeRects()
					checkFrameAt(t, at, name+" compose", f, true)
				}
				plain := g.plain()
				if strings.ContainsAny(plain, "\x1b\t\u202e") {
					t.Errorf("%s: control characters reached the screen", name)
				}
				content := m.View().Content
				for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
					if got := placed(content, sz[0], sz[1], method); got != plain {
						t.Errorf("%s: width method %v places it otherwise:\n%s\nwant:\n%s", name, method, got, plain)
					}
				}
				if t.Failed() {
					t.Logf("%s:\n%s", name, plain)
					return
				}
			}
		}
	}
}
