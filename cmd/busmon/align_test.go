package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The alignment suite (spec §8.2): the real screen and its overlays, drawn at
// every supported size with hostile content, must keep every frame straight —
// corners where they belong, a left border on every row, a right border (or
// the scrollbar, inside the corners) on every row.

var alignSizes = [][2]int{{160, 45}, {120, 40}, {120, 12}, {119, 40}, {119, 12}, {160, 13}, {100, 30},
	{80, 24}, {80, 11}, {79, 24}, {64, 22}, {40, 12}, {30, 6}}

const hostileText = "日本語の説明 👩\u200d💻 e\u0301 ❤\uFE0F\ttab\nnewline \x1b[31mred [red]x[-] \u202eRTL"

func hostileShared(now time.Time, down bool) *shared {
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{
		"anthropic": {SessionPct: 80, WeeklyPct: 95}, "openai": {SessionPct: 10, WeeklyPct: 20}},
		board: map[string]bus.BoardEntry{}, health: health{ok: true, since: now}, pilot: "a00", dispatch: "up", missions: "down"}
	states := []string{"working", "idle", "blocked", "done", "", "review"}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("a%02d%s", i, strings.Repeat("x", 29))
		if i == 0 {
			name = "a00"
		}
		a := &agentState{state: states[i%len(states)], message: hostileText, stateAt: now.Add(-time.Duration(i) * time.Minute),
			queueKnown: i%3 != 0, waiting: int64(i % 4), queueFailed: i%7 == 0, gated: i % 2, armed: i%2 == 0,
			stale: i % 3, ctxRaw: []string{"608k", "", "25%", "1.2M"}[i%4], forYou: i%len(states) == 1}
		if i%4 == 1 { // client reload spec: an outdated client, observed now
			a.obsAt, a.clientUpdate = now, &bus.ClientUpdate{Client: "claude-code", Running: hostileText, Installed: hostileText, Hash: "h"}
		}
		switch i % 5 {
		case 1:
			a.wake = &bus.WakeRecord{State: "deferred"}
		case 2:
			a.wake = &bus.WakeRecord{State: "prompted", Escalated: "true"}
		case 3:
			a.wake = &bus.WakeRecord{State: "weird-state"}
		}
		st.agents[name] = a
	}
	for i := 0; i < 30; i++ {
		st.board[fmt.Sprintf("task-%02d-%s", i, hostileText)] = bus.BoardEntry{Owner: "a00", State: []string{"working", "requested", "blocked", "done"}[i%4], Updated: now.Unix()}
	}
	st.requests = []request{{Task: "t-" + hostileText, Tracked: true, From: "a00", Target: "a01", State: "blocked", BlockedReason: hostileText, Created: now}}
	st.threads = []openThread{{ID: "1-0", Type: "directive", From: "a00", Target: "a02", Message: hostileText, At: now}}
	if down {
		st.health = health{ok: false, since: now.Add(-42 * time.Second), err: hostileText}
	}
	return st
}

type alignCase struct {
	name  string
	setup func(h *harness)
}

func alignCases() []alignCase {
	feed := func(h *harness) {
		for i := 0; i < 40; i++ {
			// Some entries are for the person (the harness writes as bernard):
			// a cmd to bernard and a notice for=person every 8, so the feed
			// holds unread and superseded marks of both kinds.
			target, forWho := "a02", ""
			switch i % 8 {
			case 3:
				target = "bernard"
			case 2:
				forWho = bus.ForPerson
			}
			h.feed(bus.Event{ID: fmt.Sprintf("17916494%05d-0", i), Kind: []string{"status", "report", "notify", "cmd"}[i%4],
				Agent: "a00", From: "a01" + strings.Repeat("y", 30), Target: target, Type: "directive", State: "working",
				Message: hostileText, Full: hostileText + hostileText, Ref: []string{"", hostileText}[i%2], For: forWho})
		}
	}
	return []alignCase{
		{"main", feed},
		{"filter+selection", func(h *harness) {
			feed(h)
			h.u.filter = parseFilter("tab")
			h.key(key(tcell.KeyTab))
			h.key(key(tcell.KeyUp))
		}},
		{"agent detail", func(h *harness) { feed(h); h.key(key(tcell.KeyEnter)) }},
		{"message detail", func(h *harness) {
			feed(h)
			h.key(key(tcell.KeyTab))
			h.key(key(tcell.KeyUp))
			h.u.detailThread = threadState{Loaded: true}
			h.key(key(tcell.KeyEnter))
		}},
		{"tab 2", func(h *harness) { feed(h); h.key(runeKey('2')) }},
		{"tab 3", func(h *harness) { feed(h); h.key(runeKey('3')) }},
		{"tab 4", func(h *harness) { feed(h); h.key(runeKey('4')) }},
		{"help", func(h *harness) { h.key(key(tcell.KeyF1)) }},
		{"requests overlay", func(h *harness) { h.key(key(tcell.KeyF2)) }},
		{"composing", func(h *harness) { feed(h); h.key(runeKey('i')); h.typeText(hostileText[:12]) }},
		{"compose window", func(h *harness) {
			feed(h)
			h.key(runeKey('I'))
			h.u.comp.area.SetText(hostileText+"\n"+hostileText, true)
		}},
	}
}

// checkFrame asserts one frame's geometry on the simulation screen.
func checkFrame(t *testing.T, sim tcell.SimulationScreen, name string, r Rect, scrollable bool) {
	t.Helper()
	if r.W < 2 || r.H < 2 {
		return
	}
	at := func(x, y int) string { s, _, _ := sim.Get(x, y); return s }
	x1, y1 := r.X+r.W-1, r.Y+r.H-1
	for _, c := range []struct {
		x, y int
		want string
	}{{r.X, r.Y, glyphTopLeft}, {x1, r.Y, glyphTopRight}, {r.X, y1, glyphBottomLeft}, {x1, y1, glyphBottomRight}} {
		if got := at(c.x, c.y); got != c.want {
			t.Errorf("%s: corner (%d,%d) = %q, want %q", name, c.x, c.y, got, c.want)
		}
	}
	for y := r.Y + 1; y < y1; y++ {
		if got := at(r.X, y); got != glyphVertical {
			t.Errorf("%s: left border (%d,%d) = %q", name, r.X, y, got)
		}
		got := at(x1, y)
		ok := got == glyphVertical || (scrollable && (got == GlyphBlocked || got == GlyphDown || got == glyphThumb))
		if !ok {
			t.Errorf("%s: right border (%d,%d) = %q", name, x1, y, got)
		}
	}
	for x := r.X + 1; x < x1; x++ {
		if got := at(x, y1); got != glyphHorizontal {
			t.Errorf("%s: bottom border (%d,%d) = %q", name, x, y1, got)
			break
		}
	}
}

func TestAlignmentSuite(t *testing.T) {
	saved := th
	defer func() { th = saved }()
	for _, palette := range []string{"dark", "light", "16", "none"} {
		var err error
		name, colors := palette, 1<<24
		switch palette {
		case "16":
			name, colors = "dark", 16
		case "none":
			name = "dark"
		}
		th, err = ResolveTheme(name, "", "", palette == "none", colors)
		if err != nil {
			t.Fatal(err)
		}
		checkPick(t, palette) // the --pick picker: its own screen, same frames and rules
		if t.Failed() {
			return
		}
		for _, down := range []bool{false, true} {
			for _, c := range alignCases() {
				for _, sz := range alignSizes {
					name := fmt.Sprintf("%s/%s/down=%v/%dx%d", palette, c.name, down, sz[0], sz[1])
					h := newHarness(t, hostileShared(time.Now(), down))
					h.draw(sz[0], sz[1])
					c.setup(h)
					screen := h.draw(sz[0], sz[1])
					l := h.u.layout
					if l.Tier == TierTooSmall {
						continue
					}
					if c.name == "main" || c.name == "filter+selection" || c.name == "composing" {
						for _, p := range l.Visible {
							checkFrame(t, h.sim, fmt.Sprintf("%s panel %d", name, p), l.panelRect(p), true)
						}
					}
					if h.u.ov != ovNone {
						x, y, w, hh := h.u.overlay.GetRect()
						checkFrame(t, h.sim, name+" overlay", Rect{x, y, w, hh}, true)
					}
					if h.u.comp.open {
						checkFrame(t, h.sim, name+" compose", composeRect(sz[0], sz[1], l.Header.H), false)
					}
					first := strings.SplitN(screen, "\n", 2)[0]
					if !strings.Contains(first, " bus") || !strings.Contains(first, " disp") {
						t.Errorf("%s: header lacks health or dispatch: %q", name, first)
					}
					// No primitive writes outside the screen (a plain
					// SimulationScreen drops such writes silently).
					rec := newRecScreen(t, sz[0], sz[1], Rect{0, 0, sz[0], sz[1]})
					h.u.scr = rec
					h.u.pages.Draw(rec)
					h.u.scr = h.sim
					for _, b := range rec.bad {
						t.Errorf("%s: write off screen %s", name, b)
					}
					if strings.ContainsAny(screen, "\x1b\t\u202e") {
						t.Errorf("%s: control characters reached the screen", name)
					}
					if t.Failed() {
						t.Logf("%s:\n%s", name, screen)
						return
					}
				}
			}
		}
	}
}
