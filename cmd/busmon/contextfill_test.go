package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func freshFill(pct float64) *bus.ContextFill {
	at := time.Now().UnixMilli()
	return &bus.ContextFill{Pct: pct, Session: "s1", ObservedAt: at - 60_000, At: at}
}

// The fill is read with the dispatcher facts; a failed read keeps the last
// value, an agent no longer published reads unknown.
func TestPollReadsTheContextFill(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"coder": {State: "idle", TS: 1, Pane: "p1", PID: 7}, "codex": {State: "idle", TS: 1, Pane: "p2", PID: 8}},
		contexts: map[string]bus.ContextFill{"coder": *freshFill(85)}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	tick := 0
	poll := func() {
		for i := 0; i < 5; i++ { // the dispatcher facts are read every 5th tick
			pollOnce(context.Background(), f, st, tick)
			tick++
		}
	}
	poll()
	if c := st.agents["coder"].ctxFill; c == nil || c.Pct != 85 || st.agents["codex"].ctxFill != nil {
		t.Fatalf("coder %+v codex %+v", c, st.agents["codex"].ctxFill)
	}
	f.contextErr = errors.New("timeout")
	poll()
	if c := st.agents["coder"].ctxFill; c == nil || c.Pct != 85 {
		t.Fatalf("a failed read dropped the fill: %+v", c)
	}
	f.contextErr, f.contexts = nil, nil
	poll()
	if c := st.agents["coder"].ctxFill; c != nil {
		t.Fatalf("an unpublished fill is kept: %+v", c)
	}
}

// The wide row shows the percent beside the raw tokens, in the attention
// colour at the alert threshold; an unknown or aged fill shows no percent.
func TestAgentRowShowsTheContextFill(t *testing.T) {
	now := time.Now()
	aged := freshFill(95)
	aged.ObservedAt = aged.At - bus.ContextMaxAge.Milliseconds() - 1
	for _, c := range []struct {
		fill *bus.ContextFill
		raw  string
		want string
		role Role
	}{
		{freshFill(85), "180k", "180k 85%", RoleAttention},
		{freshFill(42.6), "41000", "41k 43%", RoleText},
		{freshFill(85), "", "85%", RoleAttention},
		{aged, "180k", "180k", RoleText},
		{nil, "180k", "180k", RoleText},
		{nil, "", GlyphDash, RoleMuted},
	} {
		a := &agentState{state: "idle", stateAt: now, ctxRaw: c.raw, ctxFill: c.fill}
		cell := ctxCell(a)
		if Line(cell).text() != c.want || cell[len(cell)-1].Role != c.role {
			t.Errorf("%+v %q: %q", c.fill, c.raw, Line(cell).text())
		}
		row := agentRow("coder", a, false, TierWide, 160, now).text()
		if !strings.Contains(row, c.want) {
			t.Errorf("row lacks %q: %q", c.want, row)
		}
	}
}

// The agent page states the fill, or that it is unknown, never 0.
func TestAgentDetailShowsTheContextFill(t *testing.T) {
	now := time.Now()
	body := func(a *agentState) string {
		_, lines := agentDetail("coder", a, false, nil, nil, now)
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l.text() + "\n")
		}
		return b.String()
	}
	if got := body(&agentState{state: "idle", stateAt: now, ctxFill: freshFill(85)}); !strings.Contains(got, "85% of the context window") || !strings.Contains(got, "at or above the 80% alert") {
		t.Errorf("known fill:\n%s", got)
	}
	if got := body(&agentState{state: "idle", stateAt: now}); !strings.Contains(got, "Fill") || !strings.Contains(got, "unknown") || strings.Contains(got, "0%") {
		t.Errorf("unknown fill:\n%s", got)
	}
}
