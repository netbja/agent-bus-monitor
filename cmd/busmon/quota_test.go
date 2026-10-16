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
	// An idle (or working) agent held by its quota reads "quota" in the
	// state column, and the flag would only repeat it.
	if _, word, _, _ := stateLook(st.agents["coder"]); word != "quota" {
		t.Fatalf("held idle agent shows %q, want quota", word)
	}
	for _, f := range agentFlags(st.agents["coder"], false) {
		if f.Word == "held" {
			t.Fatalf("held flag repeats the quota state: %+v", f)
		}
	}
	// A blocked agent keeps its state; held is then a flag.
	blocked := &agentState{state: "blocked", held: true}
	if _, word, _, _ := stateLook(blocked); word != "blocked" {
		t.Fatalf("blocked held agent shows %q", word)
	}
	if fs := agentFlags(blocked, false); len(fs) == 0 || fs[0].Word != "held" {
		t.Fatalf("blocked held agent flags %+v", fs)
	}
}

func TestPausedProjectShowsInTheStateColumn(t *testing.T) {
	until := time.Now().Add(time.Hour).UnixMilli()
	f := &fakeReader{calls: map[string]int{}, pause: &bus.PauseRecord{Until: until, Reason: "quota anthropic", By: "dispatch", Notify: "master"},
		snaps: map[string]bus.AgentSnapshot{"master": {State: "working", TS: 1}, "foureyes": {State: "waiting", TS: 1}, "coder": {State: "done", TS: 1}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 1)
	for name, want := range map[string]string{"master": "paused", "foureyes": "waiting", "coder": "done"} {
		if _, word, _, _ := stateLook(st.agents[name]); word != want {
			t.Errorf("%s shows %q while paused, want %q", name, word, want)
		}
	}
	_, body := agentDetail("master", st.agents["master"], false, nil, nil, time.Now())
	if !strings.Contains(body, "working") || !strings.Contains(body, "shown as paused") || !strings.Contains(body, "the project is paused") {
		t.Errorf("detail lost the declared state or the reason:\n%s", body)
	}
	f.pause = nil
	pollOnce(context.Background(), f, st, 1)
	if _, word, _, _ := stateLook(st.agents["master"]); word != "working" {
		t.Errorf("after the resume master shows %q, want working", word)
	}
}

func TestAgentDetailNamesItsQuota(t *testing.T) {
	now := time.Now()
	b := bus.BudgetSnapshot{Source: "codex app-server", Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 96, ResetsAt: now.Add(time.Hour).Unix(), ObservedAt: now.Add(-time.Minute).UnixMilli()}}}
	got := quotaDetail("openai", b, true, now.Add(time.Hour), true, now)
	for _, want := range []string{"Quota", "codex 5h 96%", "resets ", "observed 1m ago", "source: codex app-server", "held by the dispatcher"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if got := quotaDetail("", b, false, time.Time{}, false, now); !strings.Contains(got, "never held") {
		t.Fatalf("no provider: %s", got)
	}
}

func TestHeaderPrefersFreshWindowsAndShowsAStickyHold(t *testing.T) {
	now := time.Now()
	b := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{
		{Name: "7d", UsedPct: 97, ResetsAt: now.Add(72 * time.Hour).Unix(), ObservedAt: now.Add(-5 * time.Hour).UnixMilli()},
		{Name: "5h", UsedPct: 10, ResetsAt: now.Add(time.Hour).Unix(), ObservedAt: now.UnixMilli()}}}
	if got := quotaSeg("anthropic", b, time.Time{}, false, "", now).text(); got != "claude 5h 10%" {
		t.Fatalf("header %q: a stale window outranked a fresh one", got)
	}
	unknown := bus.BudgetSnapshot{Unknown: true}
	if got := quotaSeg("openai", unknown, now.Add(time.Hour), true, "", now).text(); !strings.Contains(got, "codex ? held") {
		t.Fatalf("sticky hold header %q", got)
	}
	if got := quotaDetail("openai", unknown, true, now.Add(time.Hour), true, now); !strings.Contains(got, "still held by the dispatcher") || strings.Contains(got, "never holds") {
		t.Fatalf("sticky hold detail:\n%s", got)
	}
}
