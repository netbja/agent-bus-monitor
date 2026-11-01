package main

import (
	"context"

	"github.com/netbja/agent-bus-monitor/bus"
	"strings"
	"testing"
	"time"
)

func hasFlag(a *agentState, w string) bool {
	for _, f := range agentFlags(a, false) {
		if f.Word == w {
			return true
		}
	}
	return false
}

// idle?: working (declared or flipped) while a fresh observation has seen the
// pane ready for display.idle_hint_after; a hint, never a relabel.
func TestIdleHintFlag(t *testing.T) {
	now := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	base := func() *agentState {
		return &agentState{state: "working", obsState: "idle", obsAt: now.Add(-5 * time.Second),
			obsSince: now.Add(-12 * time.Minute), judgedAt: now, idleHintAfter: 10 * time.Minute}
	}
	if a := base(); !hasFlag(a, "idle?") || a.state != "working" {
		t.Fatalf("ready 12 min under working: %s", flagWords(agentFlags(a, false)))
	}
	for name, change := range map[string]func(a *agentState){
		"under the threshold": func(a *agentState) { a.obsSince = now.Add(-5 * time.Minute) },
		"busy":                func(a *agentState) { a.obsBusy = true },
		"blocked":             func(a *agentState) { a.obsState = "blocked" },
		"unknown":             func(a *agentState) { a.obsState = "unknown" },
		"stale":               func(a *agentState) { a.obsAt = now.Add(-2 * observedFresh) },
		"declared idle":       func(a *agentState) { a.state = "idle" },
		"0 = off":             func(a *agentState) { a.idleHintAfter = 0 },
	} {
		a := base()
		change(a)
		if hasFlag(a, "idle?") {
			t.Errorf("%s: idle? shown", name)
		}
	}
}

// A flipped state's kept message reads muted with (turn) after it, in the
// row and on the agent's page.
func TestTurnMarkOnAFlippedState(t *testing.T) {
	a := &agentState{state: "working", message: "approved; nothing pending", auto: true}
	row := agentRow("coder", a, false, TierWide, 200, time.Now()).text()
	if !strings.Contains(row, "approved; nothing pending (turn)") {
		t.Fatalf("row: %q", row)
	}
	a.auto = false
	if strings.Contains(agentRow("coder", a, false, TierWide, 200, time.Now()).text(), "(turn)") {
		t.Fatal("a declared state carries the mark")
	}
}

// The setting reaches every agent through the snapshot.
func TestIdleHintSettingReachesTheAgents(t *testing.T) {
	st := &shared{agents: map[string]*agentState{"coder": {state: "working"}}, idleHint: 7 * time.Minute}
	if got := takeSnapshot(st, time.Now()).agents["coder"].idleHintAfter; got != 7*time.Minute {
		t.Fatalf("idleHintAfter %v", got)
	}
}

// The (turn) mark comes from the snapshot the poll reads: a flip shows it, a
// declaration clears it, even one whose ts is older or equal (mergeSnapshot
// lets any declaration replace a flip), and a flip at the same ms as the
// declaration it replaces is applied too.
func TestPollAppliesFlipsAndDeclarations(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	var seen time.Time
	row := func() string {
		return agentRow("coder", st.agents["coder"], false, TierWide, 200, time.Now()).text()
	}
	for i, c := range []struct {
		name string
		snap bus.AgentSnapshot
		turn bool
		want string
	}{
		{"declared", bus.AgentSnapshot{State: "working", Message: "history", TS: 100}, false, "history"},
		{"flip, same ms", bus.AgentSnapshot{State: "idle", Message: "history", TS: 100, Auto: bus.AutoTurn}, true, "idle"},
		{"flip, newer", bus.AgentSnapshot{State: "working", Message: "history", TS: 101, Auto: bus.AutoTurn}, true, "working"},
		{"declaration, older", bus.AgentSnapshot{State: "idle", Message: "done", TS: 99}, false, "done"},
		{"stale flip after it", bus.AgentSnapshot{State: "working", Message: "history", TS: 98, Auto: bus.AutoTurn}, false, "done"},
	} {
		f.snaps["coder"] = c.snap
		pollOnce(context.Background(), f, st, i)
		a := st.agents["coder"]
		if a.auto != c.turn || strings.Contains(row(), "(turn)") != c.turn || !strings.Contains(row(), c.want) {
			t.Fatalf("%s: auto %v, state %q, row %q", c.name, a.auto, a.state, row())
		}
		if a.lastSeen.Before(seen) {
			t.Fatalf("%s: lastSeen went back to %v", c.name, a.lastSeen)
		}
		seen = a.lastSeen
	}
}
