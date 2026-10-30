package main

import (
	"slices"
	"testing"
)

// busmon-keys: Left and Right step the lower panel's tabs, and w writes to master when no
// question is open.

// Right and Left step the lower panel's tabs, wrapping at both ends as herdr's next and previous
// tab do (herdr src/client/shell/actions.rs: rem_euclid).
func TestArrowsStepTheLowerTabs(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{200, 50}, {100, 40}, {70, 40}} {
		m := hostileShell(t, size[0], size[1], false)
		tabs := lowerTabs // the digit that shows Activity: the lower tabs', or the narrow tier's views
		if m.layout.Tier == TierNarrow {
			tabs = narrowPanels
		}
		m = keys(t, m, rune('1'+slices.Index(tabs, PanelActivity)))
		if m.ui.focus != PanelActivity {
			t.Fatalf("%v: 1 focused %v", size, m.ui.focus)
		}
		n := len(lowerTabs)
		for i := 1; i <= n; i++ {
			if m = keys(t, m, kRight); m.ui.focus != lowerTabs[i%n] {
				t.Fatalf("%v: right #%d focused %v, want %v", size, i, m.ui.focus, lowerTabs[i%n])
			}
		}
		if m = keys(t, m, kLeft); m.ui.focus != lowerTabs[n-1] {
			t.Fatalf("%v: left from %v focused %v, want %v (wrap)", size, lowerTabs[0], m.ui.focus, lowerTabs[n-1])
		}
	}
}

// Outside the lower panel the arrows move nothing, and the key table offers them there neither
// (so the footer and help do not advertise them on Agents or For you).
func TestArrowsLeaveTheOtherPanelsAlone(t *testing.T) {
	pinLocal(t)
	for _, p := range []PanelID{PanelAgents, PanelForYou} {
		for _, k := range []Key{{Code: keyRight}, {Code: keyLeft}} {
			if act := lookup(ModeShell, p, TierWide, k); act == ActTabNext || act == ActTabPrev {
				t.Fatalf("on %v the arrow is bound to %v", p, act)
			}
		}
		m := hostileShell(t, 200, 50, false)
		m.ui.focus = p
		before := m.layout.Active
		if m = keys(t, m, kRight, kLeft, kRight); m.ui.focus != p || m.layout.Active != before {
			t.Fatalf("on %v: focus %v, active tab %v -> %v", p, m.ui.focus, before, m.layout.Active)
		}
	}
}

// w answers an open question, as ! does; with none open it opens the compose window to master.
func TestWWritesToMasterWhenNoQuestionIsOpen(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 200, 50)
	if len(openAsks(m.snap.asks)) == 0 {
		t.Fatal("the fixture has no open ask")
	}
	if m = keys(t, m, 'w'); m.rd.top() == nil || m.rd.top().kind != ovAsk || m.ui.compose != nil {
		t.Fatalf("w with an open ask: page %v, compose %v", m.rd.top(), m.ui.compose)
	}

	m = fixtureShell(t, 200, 50)
	m.snap.asks = nil
	if m = keys(t, m, 'w'); m.ui.compose == nil || m.ui.compose.to.value() != "@master" || m.rd.open() {
		t.Fatalf("w with no ask: compose %+v", m.ui.compose)
	}

	m = fixtureShell(t, 200, 50) // ! and F4 still only answer
	m.snap.asks = nil
	if m = keys(t, m, '!'); m.ui.compose != nil {
		t.Fatal("! with no ask opened the compose window")
	}
}

// On a page w does the same; Left and Right keep their page roles.
func TestWOnAPageWritesToMasterWhenNoQuestionIsOpen(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 200, 50)
	m.snap.asks = nil
	m = keys(t, m, kF1)
	if !m.rd.open() {
		t.Fatal("F1 opened no page")
	}
	if m = keys(t, m, 'w'); m.ui.compose == nil || m.ui.compose.to.value() != "@master" || m.rd.open() {
		t.Fatalf("w on a page with no ask: compose %+v, reader open %v", m.ui.compose, m.rd.open())
	}
}
