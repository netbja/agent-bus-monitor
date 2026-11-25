package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-three-fixes 1: /resume on a project that is neither paused nor
// suspended asks nothing (no red "Run agentbus … resume --yes? y / n"):
// it says there is nothing to resume, not as an error. Paused or
// suspended, it asks as before; an unreadable pause state still asks.
func TestPaletteResumeOnlyWhenPaused(t *testing.T) {
	w := newPalCWorld(t, "charlie")
	w.run(t, "/resume")
	if w.m.ui.sleepAsk != nil || w.m.ui.flashErr || w.m.ui.flash != "the project is not paused or suspended: nothing to resume" || len(w.runs) != 0 {
		t.Fatalf("not paused: ask %v, flash %q (err %v), runs %q", w.m.ui.sleepAsk, w.m.ui.flash, w.m.ui.flashErr, w.runs)
	}
	for name, set := range map[string]func(*palCWorld){
		"paused":     func(w *palCWorld) { w.m.snap.pause = &bus.PauseRecord{By: "charlie", At: time.Now().UnixMilli()} },
		"suspended":  func(w *palCWorld) { w.m.snap.pause = &bus.PauseRecord{By: "charlie", Kind: bus.PauseKindSuspend} },
		"unreadable": func(w *palCWorld) { w.m.snap.pauseFailed = true },
	} {
		w := newPalCWorld(t, "charlie")
		set(w)
		w.run(t, "/resume")
		if w.m.ui.sleepAsk == nil || !strings.Contains(dialogText(w.m), "resume --yes") {
			t.Errorf("%s: no confirmation: dialog %q", name, dialogText(w.m))
		}
	}
}

// busmon-three-fixes 2: G (live) with auto-scroll off in the settings says
// so and names the setting; with it on, G says nothing.
func TestLiveKeySaysAutoScrollIsOff(t *testing.T) {
	pinLocal(t)
	m := shellWith(t, 140, 40, shellConfig{settings: busmonSettings{NoFollow: true}}, false)
	m = moreLines(t, m, 40, time.Second)
	m = keys(t, m, '3', '1', 'g', 'G')
	if !strings.Contains(m.ui.flash, "display.follow") || !strings.Contains(m.ui.flash, "off") || m.ui.flashErr {
		t.Fatalf("follow off: flash %q (err %v)", m.ui.flash, m.ui.flashErr)
	}
	m = moreLines(t, fixtureShell(t, 140, 40), 40, time.Second)
	if m = keys(t, m, '3', '1', 'g', 'G'); m.ui.flash != "" || !m.lists.activity.Live() {
		t.Fatalf("follow on: flash %q, live %v", m.ui.flash, m.lists.activity.Live())
	}
}

// busmon-three-fixes 3: the Requests view (r, /requests) is titled
// "Requests", with the tab it came from as the way back, not the tab bar
// ("1 Activity  2 Board …"), at every tier, the narrow one included.
func TestRequestsViewHasItsOwnTitle(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{190, 44}, {120, 40}, {80, 40}, {60, 40}} {
		m := reqShell(t, sz[0], sz[1], nil)
		screen := m.grid().plain()
		tabs := strings.Contains(screen, "2 Board") || strings.Contains(screen, "1 Activity") || // the lower frame's tab bar
			strings.Contains(screen, "4 Board") || strings.Contains(screen, "1 For you") // the narrow tier's tab row
		if !strings.Contains(screen, "Requests") || tabs {
			t.Errorf("%dx%d (tier %v):\n%s", sz[0], sz[1], m.layout.Tier, screen)
		}
	}
}

// With no tab bar drawn on the Requests view, a click where a tab used to
// be (the lower frame's title row; the narrow tier's tab row) switches to
// no tab: the view stays.
func TestRequestsViewTitleClickSwitchesNothing(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{120, 40}, {60, 40}} {
		m := reqShell(t, sz[0], sz[1], nil)
		y := m.layout.rect(PanelRequests).Y
		if m.layout.Tier == TierNarrow {
			y = m.layout.Tabs.Y
		}
		for x := 0; x < sz[0]; x += 3 {
			n := step(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if n.ui.focus != PanelRequests {
				t.Fatalf("%dx%d: a click at %d,%d left Requests for %v", sz[0], sz[1], x, y, n.ui.focus)
			}
		}
	}
}
