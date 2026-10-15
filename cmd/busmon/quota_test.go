package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestQuotaGauges(t *testing.T) {
	now := time.Now()
	reset := now.Add(2 * time.Hour).Unix()
	fresh := bus.BudgetSnapshot{ObservedAt: now.UnixMilli(), Windows: []bus.BudgetWindow{
		{Name: "5h", UsedPct: 82, ResetsAt: reset}, {Name: "7d", UsedPct: 40, ResetsAt: now.Add(72 * time.Hour).Unix()},
		{Name: "old", UsedPct: 99, ResetsAt: now.Add(-time.Minute).Unix()}}}
	if got := quotaSeg("anthropic", fresh, time.Time{}, false, "~25m", now).text(); got != "claude 5h 82% ~25m" {
		t.Fatalf("fresh %q", got)
	}
	if got := quotaSeg("openai", fresh, now.Add(time.Hour), true, "", now).text(); !strings.HasPrefix(got, "codex 5h 82% held ") {
		t.Fatalf("held %q", got)
	}
	aged := fresh
	aged.ObservedAt = now.Add(-time.Hour).UnixMilli()
	if got := quotaSeg("xai", aged, time.Time{}, false, "~5m", now).text(); got != "grok 5h 82% ?" {
		t.Fatalf("aged %q", got)
	}
	if got := quotaSeg("moonshot", bus.BudgetSnapshot{Unknown: true}, time.Time{}, false, "", now).text(); got != "kimi ?" {
		t.Fatalf("unknown %q", got)
	}
	if quotaSeg("local", bus.BudgetSnapshot{Unlimited: true}, time.Time{}, false, "", now) != nil {
		t.Fatal("an unlimited provider has a gauge")
	}
}

func TestPaceIsAnEstimateFromWhatBusmonSaw(t *testing.T) {
	now := time.Now()
	reset := now.Add(4 * time.Hour).Unix()
	snap := func(pct float64) map[string]bus.BudgetSnapshot {
		return map[string]bus.BudgetSnapshot{"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: pct, ResetsAt: reset}}}}
	}
	var p paceTracker
	p.observe(snap(50), now)
	p.observe(snap(60), now.Add(10*time.Minute)) // 1 %/min → 40 min left
	if got := p.last["anthropic"]; got != "~40m" {
		t.Fatalf("pace %q", got)
	}
	p.observe(snap(10), now.Add(11*time.Minute)) // a drop: start over, no estimate
	if _, ok := p.last["anthropic"]; ok {
		t.Fatal("estimate kept after a drop")
	}
}

func TestHeldAgentsAreFlagged(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	f := &fakeReader{calls: map[string]int{}, marks: map[string]string{"hold:openai": strconv.FormatInt(reset, 10)},
		snaps: map[string]bus.AgentSnapshot{"coder": {State: "idle", TS: 1, Provider: "openai"}, "master": {State: "idle", TS: 1, Provider: "anthropic"}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 1)
	if !st.agents["coder"].held || st.agents["master"].held {
		t.Fatalf("held: coder %v master %v", st.agents["coder"].held, st.agents["master"].held)
	}
	flags := agentFlags(st.agents["coder"], false)
	if len(flags) == 0 || flags[0].Word != "held" {
		t.Fatalf("flags %+v", flags)
	}
}
