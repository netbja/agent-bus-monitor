package main

import (
	"fmt"
	"strings"
	"testing"

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
	st.person = "bernard"
	m := newShell(shellConfig{project: "demo-" + hostileText, self: "bernard"})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	var events []bus.Event
	for i := 0; i < 40; i++ {
		target, forWho := "a02", ""
		switch i % 8 {
		case 3:
			target = "bernard"
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
func TestShellAlignmentSuite(t *testing.T) {
	pinLocal(t)
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
	}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, down := range []bool{false, true} {
			for _, sz := range shellAlignSizes {
				for _, tab := range lowerTabs {
					views := []PanelID{PanelForYou}
					if sz[0] < mediumMinWidth {
						views = narrowPanels
					}
					for _, view := range views {
						name := fmt.Sprintf("%s-%d/down=%v/%dx%d/tab %d/view %d", pal.name, pal.colors, down, sz[0], sz[1], tab, view)
						m := hostileShell(t, sz[0], sz[1], down)
						m.ui.tab, m.ui.view = tab, view
						m.paint = newPainter(th)
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

// Reading keeps the frames straight too: a hostile line expanded in place,
// then open in the reader, at every size and palette.
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
			for _, state := range []string{"expanded", "reader"} {
				if state == "reader" {
					m = keys(t, m, kEnter)
				}
				name := fmt.Sprintf("%s-%d/%dx%d/%s", pal.name, pal.colors, sz[0], sz[1], state)
				if (state == "expanded") != (m.ui.expanded != "" && !m.rd.open()) {
					t.Fatalf("%s: expanded %q, reader %v", name, m.ui.expanded, m.rd.open())
				}
				g := m.grid()
				at := func(x, y int) string { return g.at(x, y).text }
				for _, p := range m.layout.Visible {
					if m.rd.open() && (p == m.layout.Active || m.layout.Tier == TierNarrow) {
						continue
					}
					checkFrameAt(t, at, name+" panel "+fmt.Sprint(p), m.layout.rect(p), true)
				}
				if m.rd.open() {
					checkFrameAt(t, at, name+" reader", m.readerRect(), true)
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
