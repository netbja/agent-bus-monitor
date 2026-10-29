package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
		if i%3 == 1 { // context-alert: the widest fill cell, tokens and 100%
			a.ctxFill = &bus.ContextFill{Pct: 100, Session: "s", ObservedAt: now.UnixMilli(), At: now.UnixMilli()}
		}
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
	for i := 0; i < 12; i++ { // the Requests view's rows: every section, hostile senders, targets and texts
		from, to, typ, msg := "a0"+fmt.Sprint(i%3)+hostileText, "a0"+fmt.Sprint(i%4), "directive", hostileText
		switch i % 4 {
		case 1:
			from = "charlie"
		case 2:
			from = "dispatch"
		case 3:
			msg = reportPrefix("1-1") + hostileText
		}
		st.cmdHistory = append(st.cmdHistory, bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-%d", now.Add(-time.Minute).UnixMilli(), i), Type: typ,
			From: from, Target: to, Message: msg})
	}
	if down {
		st.health = health{ok: false, since: now.Add(-42 * time.Second), err: hostileText}
	}
	return st
}

// checkFrameAt asserts one frame's geometry on any grid of cells.
func checkFrameAt(t *testing.T, at func(x, y int) string, name string, r Rect, scrollable bool) {
	t.Helper()
	if r.W < 2 || r.H < 2 {
		return
	}
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
