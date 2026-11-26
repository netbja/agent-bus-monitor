package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// tui-rendering §6: the full matrix for the screens whose visuals change
// (Agents and the header, Tokens, Work's Kanban, the agent page): fine and
// basic, dark, light, 16 colours and NO_COLOR, at 80x25 and 140x40.
func TestRenderingMatrixGoldens(t *testing.T) {
	pinLocal(t)
	pinAlert(t, 80)
	pinUrgent(t, 90)
	palettes := []struct {
		name, theme string
		noColor     bool
		colors      int
	}{{"dark", "dark", false, 1 << 24}, {"light", "light", false, 1 << 24}, {"16", "dark", false, 16}, {"nocolor", "dark", true, 1 << 24}}
	screens := map[string]func(t *testing.T, w, h int) Model{
		"agents": func(t *testing.T, w, h int) Model { return fixtureShell(t, w, h) },
		"tokens": tokShell,
		"kanban": func(t *testing.T, w, h int) Model {
			m := fixtureShell(t, w, h)
			m.cfg.settings.WorkLayout = "kanban"
			m.snap.requests = append(m.snap.requests, inHandReq("tag-release", "release", 30*time.Minute)) // a card with its progress
			for i := range 6 {
				m.snap.requests = append(m.snap.requests, doneReq(fmt.Sprintf("done%d", i), "release", time.Hour, time.Duration(i+1)*time.Hour))
			}
			return workNext(t, m)
		},
		"agent": func(t *testing.T, w, h int) Model {
			m := fixtureShell(t, w, h)
			m.pushAgent("coder")
			return m
		},
	}
	for _, screen := range []string{"agents", "tokens", "kanban", "agent"} {
		for _, mode := range []finedraw.Mode{finedraw.ModeFine, finedraw.ModeBasic} {
			for _, pal := range palettes {
				for _, size := range [][2]int{{80, 25}, {140, 40}} {
					name := fmt.Sprintf("render-%s-%s-%s-%dx%d.ansi", screen, mode, pal.name, size[0], size[1])
					t.Run(name, func(t *testing.T) {
						withRendering(t, mode)
						withPalette(t, pal.theme, pal.noColor, pal.colors)
						m := screens[screen](t, size[0], size[1])
						m.paint = newPainter(th)
						checkGolden(t, name, m.View().Content+"\n")
					})
				}
			}
		}
	}
}
