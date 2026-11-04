package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The packet's route: from master's node down onto the bus, along it to
// coder's node, then back the same way; every frame one cell from the last.
func TestAboutRoute(t *testing.T) {
	r := aboutRoute()
	if r[0] != (aboutPoint{aboutMasterX, 0}) || r[len(r)-1] != (aboutPoint{aboutMasterX, 0}) {
		t.Fatalf("route starts %v, ends %v", r[0], r[len(r)-1])
	}
	reached := false
	for i := 1; i < len(r); i++ {
		dx, dy := r[i].x-r[i-1].x, r[i].y-r[i-1].y
		if dx*dx+dy*dy != 1 {
			t.Fatalf("frame %d jumps from %v to %v", i, r[i-1], r[i])
		}
		if r[i] == (aboutPoint{aboutCoderX, 0}) {
			reached = true
		}
	}
	if !reached {
		t.Fatal("the packet never reaches coder")
	}
}

// The drawing says where the run is: ready before, the packet on its way
// (a directive out, a reply back), delivered once done, no packet then.
func TestAboutBody(t *testing.T) {
	v := aboutVersions{Lines: []string{"busmon dev", "agentbus v0.26.1"}}
	text := func(step int) string { return tokLinesText(aboutBody(aboutState{step: step, versions: v, loaded: true}, 80)) }
	if b := text(0); !strings.Contains(b, "bus ready") || strings.Contains(b, GlyphWorking) || !strings.Contains(b, "agentbus v0.26.1") {
		t.Fatalf("step 0:\n%s", b)
	}
	if b := text(3); !strings.Contains(b, "a directive") || !strings.Contains(b, GlyphWorking) {
		t.Fatalf("on its way:\n%s", b)
	}
	if b := text(len(aboutRoute()) - 3); !strings.Contains(b, "a reply") {
		t.Fatalf("coming back:\n%s", b)
	}
	b := text(len(aboutRoute()))
	if !strings.Contains(b, "delivered") || strings.Contains(b, GlyphWorking) || !strings.Contains(b, "master") || !strings.Contains(b, "coder") {
		t.Fatalf("done:\n%s", b)
	}
	for _, l := range aboutBody(aboutState{step: 5, versions: v, loaded: true}, 40) {
		if l.Width() > 40 {
			t.Fatalf("a line is %d cells at 40", l.Width())
		}
	}
}

func aboutShell(t *testing.T) Model {
	t.Helper()
	m := fixtureShell(t, 120, 40)
	m.cfg.aboutAgentbus = func() string { return "agentbus version v9.9.9" }
	return m
}

// runAbout feeds the commands an Update returned until a tick would wait:
// the versions are read, a tick is returned as itself.
func runAbout(t *testing.T, m Model, cmd tea.Cmd) (Model, []aboutTickMsg) {
	t.Helper()
	var ticks []aboutTickMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case aboutTickMsg:
			ticks = append(ticks, msg)
		case aboutVersionsMsg:
			next, more := m.Update(msg)
			m = next.(Model)
			walk(more)
		}
	}
	walk(cmd)
	return m, ticks
}

// A opens About: the versions read in a command (y copies them), a tick
// each step while it plays; r replays from the start and an earlier run's
// tick changes nothing; a closed page ends the chain.
func TestAboutPage(t *testing.T) {
	m := aboutShell(t)
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'A', Text: "A"}))
	m, ticks := runAbout(t, next.(Model), cmd)
	p := m.rd.top()
	if p == nil || p.kind != ovAbout {
		t.Fatal("A opened no About page")
	}
	if !strings.Contains(p.plain, "agentbus version v9.9.9") || !strings.Contains(p.plain, "busmon ") || !strings.Contains(p.plain, "protocol v") {
		t.Fatalf("y copies %q", p.plain)
	}
	if len(ticks) != 1 || !m.pageOffers(ActReplay) {
		t.Fatalf("ticks %v, r offered %v", ticks, m.pageOffers(ActReplay))
	}
	old := ticks[0]
	next, cmd = m.Update(old)
	m = next.(Model)
	if m.rd.top().about.step != 1 || cmd == nil {
		t.Fatalf("a tick: step %d", m.rd.top().about.step)
	}
	next, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"}))
	m, ticks = runAbout(t, next.(Model), cmd)
	if m.rd.top().about.step != 0 || len(ticks) != 1 {
		t.Fatalf("r: step %d, ticks %d", m.rd.top().about.step, len(ticks))
	}
	next, _ = m.Update(old) // the earlier run's tick
	m = next.(Model)
	if m.rd.top().about.step != 0 {
		t.Fatal("an earlier run's tick moved the packet")
	}
	for i := 0; i < len(aboutRoute())+5; i++ {
		next, cmd = m.Update(aboutTickMsg{serial: ticks[0].serial, gen: ticks[0].gen})
		m = next.(Model)
	}
	if a := m.rd.top().about; a.step != len(aboutRoute()) || cmd != nil {
		t.Fatalf("done: step %d, still ticking %v", a.step, cmd != nil)
	}
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = next.(Model)
	if _, cmd = m.Update(ticks[0]); cmd != nil {
		t.Fatal("a closed page still ticks")
	}
}
