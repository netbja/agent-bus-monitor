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
	if got := quotaSeg("anthropic", fresh, time.Time{}, false, map[string]string{quotaWindowKey("anthropic", fresh.Windows[0]): "~25m"}, now, TierMedium).text(); got != "claude 5h 82% ~25m │ 7d 40%" {
		t.Fatalf("fresh %q", got)
	}
	if got := quotaSeg("openai", fresh, time.Unix(reset, 0), true, nil, now, TierMedium).text(); !strings.HasPrefix(got, "codex 5h 82% held ") {
		t.Fatalf("held %q", got)
	}
	aged := fresh
	aged.ObservedAt = now.Add(-time.Hour).UnixMilli()
	if got := quotaSeg("xai", aged, time.Time{}, false, nil, now, TierNarrow).text(); got != "grok 5h 82% ?" {
		t.Fatalf("aged %q", got)
	}
	if got := quotaSeg("moonshot", bus.BudgetSnapshot{Unknown: true}, time.Time{}, false, nil, now, TierNarrow).text(); got != "kimi ?" {
		t.Fatalf("unknown %q", got)
	}
	if quotaSeg("local", bus.BudgetSnapshot{Unlimited: true}, time.Time{}, false, nil, now, TierNarrow) != nil {
		t.Fatal("an unlimited provider has a gauge")
	}
}

func TestQuotaShowsBothWindowsByTierAndAttachesMarkers(t *testing.T) {
	now := time.Now()
	short := bus.BudgetWindow{Name: "5h", UsedPct: 88, ResetsAt: now.Add(time.Hour).Unix(), Minutes: 300, ObservedAt: now.UnixMilli()}
	long := bus.BudgetWindow{Name: "7d", UsedPct: 96, ResetsAt: now.Add(48 * time.Hour).Unix(), Minutes: 10080, ObservedAt: now.Add(-time.Hour).UnixMilli()}
	b := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{long, short}, ObservedAt: now.UnixMilli()}
	medium := quotaSeg("anthropic", b, time.Time{}, false, nil, now, TierMedium)
	if got, want := medium.text(), "claude 5h 88% │ 7d 96% ?"; got != want {
		t.Fatalf("medium %q want %q", got, want)
	}
	if medium[2].Role != RoleAttention || medium[5].Role != RoleMuted {
		t.Fatalf("medium colours not attached to each window: %+v", medium)
	}
	wide := quotaSeg("anthropic", b, time.Time{}, false, nil, now, TierWide)
	if got := wide.text(); !strings.HasPrefix(got, "claude 5h "+strings.Repeat(GlyphGauge, 4)+GlyphBarEmpty+" 88% │ 7d ") || !strings.Contains(got, "96% ?") {
		t.Fatalf("wide %q", got)
	}
	heldLine := quotaSeg("anthropic", b, time.Unix(short.ResetsAt, 0), true, nil, now, TierMedium)
	if !strings.Contains(heldLine.text(), "5h 88% held ") || strings.Contains(strings.Split(heldLine.text(), " │ ")[1], "held") {
		t.Fatalf("hold marker attached to wrong window: %q", heldLine.text())
	}
}

func TestPaceIsAnEstimateFromWhatBusmonSaw(t *testing.T) {
	now := time.Now()
	reset := now.Add(4 * time.Hour).Unix()
	snap := func(pct float64, at time.Time) map[string]bus.BudgetSnapshot {
		return map[string]bus.BudgetSnapshot{"anthropic": {ObservedAt: at.UnixMilli(), Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: pct, ResetsAt: reset, Minutes: 300}}}}
	}
	var p paceTracker
	p.observe(snap(50, now), now)
	p.observe(snap(60, now.Add(10*time.Minute)), now.Add(10*time.Minute)) // 1 %/min → 40 min left
	key := "anthropic|5h|" + strconv.FormatInt(reset, 10)
	if got := p.last[key]; got != "~40m" {
		t.Fatalf("pace %q", got)
	}
	p.observe(snap(10, now.Add(11*time.Minute)), now.Add(11*time.Minute)) // a drop: start over, no estimate
	if _, ok := p.last[key]; ok {
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
	b := bus.BudgetSnapshot{Source: "codex app-server", Windows: []bus.BudgetWindow{
		{Name: "5h", UsedPct: 96, ResetsAt: now.Add(time.Hour).Unix(), Minutes: 300, ObservedAt: now.Add(-time.Minute).UnixMilli()},
		{Name: "7d", UsedPct: 76, ResetsAt: now.Add(72 * time.Hour).Unix(), Minutes: 10080, ObservedAt: now.Add(-time.Minute).UnixMilli()},
	}}
	got := quotaDetail("openai", b, true, now.Add(time.Hour), true, now)
	for _, want := range []string{"Quota", "Account  codex: 5h 96%", "resets ", "shared by every agent", "source: codex app-server", "held by the dispatcher"} {
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
	if got := quotaSeg("anthropic", b, time.Time{}, false, nil, now, TierMedium).text(); got != "claude 5h 10% │ 7d 97% ?" {
		t.Fatalf("header %q: fresh/stale windows not independently displayed", got)
	}
	unknown := bus.BudgetSnapshot{Unknown: true}
	if got := quotaSeg("openai", unknown, now.Add(time.Hour), true, nil, now, TierNarrow).text(); !strings.Contains(got, "codex ? held") {
		t.Fatalf("sticky hold header %q", got)
	}
	if got := quotaDetail("openai", unknown, true, now.Add(time.Hour), true, now); !strings.Contains(got, "still held by the dispatcher") || strings.Contains(got, "never holds") {
		t.Fatalf("sticky hold detail:\n%s", got)
	}
}

// v0.15 §13 through the poll: a blocked observation of the agent's own pid
// reads waiting; the detail keeps the declared state and names the tab; an
// observation of another pid is not its prompt.
func TestPromptThroughThePoll(t *testing.T) {
	now := time.Now().UnixMilli()
	f := &fakeReader{calls: map[string]int{},
		snaps:    map[string]bus.AgentSnapshot{"master": {State: "working", TS: 1, Pane: "w2:p2", PID: 4242, PIDStart: 7}},
		observed: map[string]bus.Observation{"master": {State: "blocked", At: now, Since: now - 60000, PID: 4242}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 1)
	if _, word, _, _ := stateLook(st.agents["master"]); word != "waiting" {
		t.Fatalf("master shows %q, want waiting", word)
	}
	_, body := agentDetail("master", st.agents["master"], false, nil, nil, time.Now())
	if !strings.Contains(body, "shown as waiting") || !strings.Contains(body, "pane w2:p2") || !strings.Contains(body, "Answer it there") {
		t.Errorf("detail:\n%s", body)
	}
	f.observed = map[string]bus.Observation{"master": {State: "blocked", At: now, Since: now, PID: 999}}
	pollOnce(context.Background(), f, st, 1)
	if promptWaiting(st.agents["master"]) {
		t.Fatal("an observation of another process reads as master's prompt")
	}
}

func TestQuotaGaugeBar(t *testing.T) {
	now := time.Now()
	b := bus.BudgetSnapshot{ObservedAt: now.UnixMilli(), Windows: []bus.BudgetWindow{
		{Name: "5h", UsedPct: 80, ResetsAt: now.Add(time.Hour).Unix()}}}
	want := "claude 5h " + strings.Repeat(GlyphGauge, 4) + GlyphBarEmpty + " 80% ~25m"
	if got := quotaSeg("anthropic", b, time.Time{}, false, map[string]string{quotaWindowKey("anthropic", b.Windows[0]): "~25m"}, now, TierWide).text(); got != want {
		t.Fatalf("bar %q, want %q", got, want)
	}
	if got := quotaSeg("anthropic", b, time.Time{}, false, nil, now, TierNarrow).text(); got != "claude 5h 80%" {
		t.Fatalf("no bar in narrow: %q", got)
	}
}
