package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// idleAt is the fixture shell in the idle view at w x h, entered at shellNow+1h.
func idleAt(t testing.TB, w, h int) Model {
	t.Helper()
	m := pollAt(t, idleShell(t, w, h, time.Minute), time.Hour)
	if !m.ui.idle {
		t.Fatalf("%dx%d: not idle", w, h)
	}
	return m
}

// Layout A (the person's choice): three frames, you, team and next, with the
// big count; the strip at the bottom with tokens, the plan and the clock.
func TestIdleViewLayoutA(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{140, 40}, {80, 24}, {60, 20}} {
		m := idleAt(t, size[0], size[1])
		got := m.grid().plain()
		q := stripOf(m.forYou)
		for _, want := range []string{" you ", " team ", " next ", "█", forYouLabel(q), "foureyes", "waiting", " idle",
			"claude 62% 5h", "grok held", "16:04"} {
			if !strings.Contains(got, want) {
				t.Errorf("%dx%d: no %q:\n%s", size[0], size[1], want, got)
			}
		}
		if size[0] == 140 && (!strings.Contains(got, "release    working  tagging") || !strings.Contains(got, "1 idle")) {
			t.Errorf("140x40: not every agent not idle:\n%s", got)
		}
		if size[0] == 60 && !strings.Contains(got, "+6 more") { // the thread row takes one (idle-animation)
			t.Errorf("60x20: the cut team does not say so:\n%s", got)
		}
		if strings.Contains(got, "Activity") || strings.Contains(got, "For you") {
			t.Errorf("%dx%d: the dashboard shows through:\n%s", size[0], size[1], got)
		}
	}
}

// Too short for the frames: one line per frame.
func TestIdleViewNarrow(t *testing.T) {
	pinLocal(t)
	m := idleAt(t, 40, 12)
	got := m.grid().plain()
	n := stripOf(m.forYou)
	for _, want := range []string{strconv.Itoa(n.decide+n.merge) + " need you", "waiting", "idle", "next"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q:\n%s", want, got)
		}
	}
}

// Nothing waiting: "nothing needs you", and no digit.
func TestIdleViewNothingNeedsYou(t *testing.T) {
	m := idleAt(t, 140, 40)
	m.snap.asks = nil
	for _, a := range m.snap.agents {
		a.state, a.openAsk, a.forYou, a.obsPrompt, a.gates, a.gated = "idle", 0, false, false, nil, 0
	}
	m.feed.feed = nil
	m.refresh()
	if len(m.forYou) != 0 {
		t.Fatalf("fixture still has %d For you items: %+v", len(m.forYou), m.forYou)
	}
	got := m.grid().plain()
	if !strings.Contains(got, "nothing needs you") || strings.Contains(got, "█") {
		t.Fatalf("empty:\n%s", got)
	}
}

// §6 drift: the block moves over the hour and never leaves the screen.
func TestIdleViewDrift(t *testing.T) {
	seen := map[[2]int]bool{}
	for k := range 120 {
		x, y := idleDrift(k, 3, 2, 20, 7)
		if x < 0 || x > 20 || y < 0 || y > 7 {
			t.Fatalf("k=%d: %d,%d out of 20x7", k, x, y)
		}
		if k > 0 {
			px, py := idleDrift(k-1, 3, 2, 20, 7)
			if absInt(x-px) > 1 || absInt(y-py) > 1 {
				t.Fatalf("k=%d: jumped from %d,%d to %d,%d", k, px, py, x, y)
			}
		}
		seen[[2]int{x, y}] = true
	}
	if len(seen) < 20 {
		t.Fatalf("only %d places in two hours", len(seen))
	}
	if x, y := idleDrift(5, 0, 0, 0, 0); x != 0 || y != 0 {
		t.Fatalf("no room: %d,%d", x, y)
	}
	m := idleAt(t, 140, 40)
	at := func(m Model) (int, int) {
		for y, row := range strings.Split(m.grid().plain(), "\n") {
			if i := strings.Index(row, "╭─ you"); i >= 0 {
				return len([]rune(row[:i])), y
			}
		}
		t.Fatal("no you frame")
		return 0, 0
	}
	x0, y0 := at(m)
	if x1, y1 := at(pollAt(t, m, time.Hour+59*time.Second)); x1 != x0 || y1 != y0 {
		t.Fatalf("moved within the minute: %d,%d to %d,%d", x0, y0, x1, y1)
	}
	m = pollAt(t, m, time.Hour+time.Minute)
	if x1, y1 := at(m); (x1 == x0 && y1 == y0) || absInt(x1-x0) > 1 || absInt(y1-y0) > 1 {
		t.Fatalf("after a minute: %d,%d to %d,%d", x0, y0, x1, y1)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// §7: without colour the view reads the same: words and digits carry it.
func TestIdleViewNoColorGolden(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", true, 1<<24)
	m := pollAt(t, idleAt(t, 140, 40), time.Hour+20*time.Second) // the thread 20 cells in (idle-animation)
	m.paint = newPainter(th)
	checkGolden(t, "idle-140x40-nocolor.ansi", m.View().Content+"\n")
}

// The next frame: the task in hand (in progress or in review first, then one
// waiting or blocked), then the first upcoming one; the strip names the plan.
func TestIdleViewNext(t *testing.T) {
	row := func(id string, st plan.State) plan.Row {
		return plan.Row{Task: plan.Task{ID: id, Title: "task " + id, Owner: "coder"}, State: st}
	}
	cases := []struct {
		rows []plan.Row
		want []string
	}{
		{[]plan.Row{row("a", plan.Done), row("b", plan.Upcoming), row("c", plan.Blocked), row("d", plan.InReview), row("e", plan.Upcoming)},
			[]string{"task d " + GlyphNone + " coder", "then: task b " + GlyphNone + " coder"}},
		{[]plan.Row{row("a", plan.Upcoming), row("b", plan.Waiting), row("c", plan.Upcoming)},
			[]string{"task b " + GlyphNone + " coder", "then: task a " + GlyphNone + " coder"}},
		{[]plan.Row{row("a", plan.Upcoming), row("b", plan.Upcoming)}, []string{"task a " + GlyphNone + " coder"}},
		{[]plan.Row{row("a", plan.Done)}, []string{"every task is done"}},
	}
	for i, c := range cases {
		var got []string
		for _, l := range idleNext(planState{loaded: true, rows: c.rows}) {
			got = append(got, l.text())
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
	if got := idleNext(planState{}); len(got) != 1 || got[0].text() != "no plan loaded" {
		t.Errorf("no plan: %v", got)
	}
	pinLocal(t)
	m := idleAt(t, 140, 40)
	m.snap.planState = planState{loaded: true, file: plan.File{Plan: "v0.29"}, rows: []plan.Row{row("a", plan.Done), row("b", plan.InProgress), row("c", plan.Upcoming)}}
	got := m.grid().plain()
	for _, want := range []string{"plan v0.29 1/3 33%", "task b", "then: task c"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q:\n%s", want, got)
		}
	}
	if l := idleStripLine(stripParts{tokens: []string{"anthropic 62% 5h"}, planName: "v0.29", planCount: "1/3 33%", clock: "16:04"}, 30); l.text() != "anthropic 62% 5h · 1/3 33%" {
		t.Errorf("narrow strip %q", l.text())
	}
	if l := idleStripLine(stripParts{tokens: []string{"anthropic 62% 5h"}, planName: "v0.29", planCount: "1/3 33%", clock: "16:04"}, 39); l.text() != "anthropic 62% 5h · plan v0.29 1/3 33%" {
		t.Errorf("no room for the clock, room for the name: %q", l.text())
	}
}

// Wide but short: the frames side by side would not fit in height, nor
// stacked: one line each, never a frame cut at the bottom.
func TestIdleViewWideButShort(t *testing.T) {
	pinLocal(t)
	got := idleAt(t, 140, 12).grid().plain()
	if strings.Contains(got, "╭─ you") || !strings.Contains(got, " need you") {
		t.Fatalf("140x12:\n%s", got)
	}
}

// foureyes P3: the big count is decide + merge, as the header chips.
func TestIdleViewCountsMerges(t *testing.T) {
	m := idleAt(t, 140, 40)
	q := stripOf(m.forYou)
	m.forYou = append(m.forYou, attentionItem{Rank: rankMerge, Key: "merge:abc123", Agent: "you", Text: "merge PR 7"})
	d := m.idleData()
	if d.need != q.decide+1 || !strings.Contains(d.parts.text(), GlyphGate+" 1 merge") {
		t.Fatalf("need %d parts %q, want %d and 1 merge", d.need, d.parts.text(), q.decide+1)
	}
}

// idle-view-providers: the strip lists every provider with a reading or a
// present agent, its tightest fresh window first by use; a stale reading says
// its age; a held one says until when; a present agent's provider with no
// reading says no data and why; an unknown reading of no present agent and
// an unlimited provider are left out. Never a number the bus does not hold.
func TestIdleTokenItems(t *testing.T) {
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	reset := now.Add(3 * time.Hour).Unix()
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{{Name: "7d", UsedPct: 65, ResetsAt: reset, ObservedAt: ms(8 * time.Minute)}, {Name: "5h", UsedPct: 16, ResetsAt: reset, ObservedAt: ms(8 * time.Minute)}}},
		"openai":    {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 2, ResetsAt: reset, ObservedAt: ms(time.Minute)}, {Name: "7d", UsedPct: 23, ResetsAt: reset, ObservedAt: ms(time.Minute)}}},
		"xai":       {Windows: []bus.BudgetWindow{{Name: "weekly", UsedPct: 71, ResetsAt: reset, ObservedAt: ms(43 * time.Hour)}}},
		"moonshot":  {Unknown: true, Source: "kimi-log"},
		"google":    {Unknown: true, Source: "agy-log"},
		"local":     {Unlimited: true},
	}
	present := map[string]bool{"anthropic": true, "openai": true, "moonshot": true, "mistral": true}
	got := idleTokenItems(budgets, nil, present, now)
	want := []string{"claude 65% 7d", "codex 23% 7d", "grok 71% 7d (1d old)", "kimi: no data (kimi-log: unknown)", "mistral: no data (no reading published)"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("items\n got %q\nwant %q", got, want)
	}

	held := map[string]int64{"openai": now.Add(2 * time.Hour).Unix()}
	got = idleTokenItems(budgets, held, present, now)
	if !slices.Contains(got, "codex held 2h00") {
		t.Errorf("held: %q", got)
	}
}

// Too narrow for every provider: the first, then +N for the rest.
func TestIdleStripCollapsesProviders(t *testing.T) {
	s := stripParts{tokens: []string{"claude 65% 7d", "codex 23% 7d", "kimi: no data (kimi-log: unknown)"}, planCount: "1/3 33%", clock: "16:04"}
	if l := idleStripLine(s, 120); l.text() != "claude 65% 7d · codex 23% 7d · kimi: no data (kimi-log: unknown) · 1/3 33%"+strings.Repeat(" ", 120-Width("claude 65% 7d · codex 23% 7d · kimi: no data (kimi-log: unknown) · 1/3 33%")-5)+"16:04" {
		t.Errorf("wide %q", l.text())
	}
	if l := idleStripLine(s, 30); l.text() != "claude 65% 7d +2 · 1/3 33%" {
		t.Errorf("narrow %q", l.text())
	}
}

// The strip reads the present agents' providers from the model: an agent on
// a provider with no reading says no data; an unlimited one says nothing.
func TestIdleStripPresentProviders(t *testing.T) {
	pinLocal(t)
	m := idleAt(t, 140, 40)
	var name string
	for _, n := range m.names(false) {
		if a := m.snap.agents[n]; a != nil && a.provider != "xai" {
			name = n
			break
		}
	}
	if name == "" {
		t.Fatal("no agent to put on another provider")
	}
	m.snap.agents[name].provider = "moonshot"
	if got := strings.Join(m.idleData().strip.tokens, " | "); !strings.Contains(got, "kimi: no data (no reading published)") {
		t.Errorf("strip %q", got)
	}
	m.snap.budgets = map[string]bus.BudgetSnapshot{"moonshot": {Unlimited: true}}
	if got := strings.Join(m.idleData().strip.tokens, " | "); strings.Contains(got, "kimi") {
		t.Errorf("an unlimited provider shows: %q", got)
	}
}

// The four kinds keep their order whatever the use: fresh, stale, held, no
// data (foureyes P3).
func TestIdleTokenItemsKindOrder(t *testing.T) {
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour).Unix()
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 5, ResetsAt: reset, ObservedAt: now.Add(-time.Minute).UnixMilli()}}},
		"xai":       {Windows: []bus.BudgetWindow{{Name: "weekly", UsedPct: 90, ResetsAt: reset, ObservedAt: now.Add(-2 * time.Hour).UnixMilli()}}},
		"openai":    {Windows: []bus.BudgetWindow{{Name: "7d", UsedPct: 99, ResetsAt: reset, ObservedAt: now.Add(-time.Minute).UnixMilli()}}},
	}
	got := idleTokenItems(budgets, map[string]int64{"openai": now.Add(time.Hour).Unix()}, map[string]bool{"moonshot": true, "anthropic": true}, now)
	want := []string{"claude 5% 5h", "grok 90% 7d (2h old)", "codex held 1h00", "kimi: no data (no reading published)"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("order\n got %q\nwant %q", got, want)
	}
}

// A dormant agent is not present: its provider without a reading shows
// nothing, as on the Agents panel without a (foureyes P3).
func TestIdleStripDormantIsNotPresent(t *testing.T) {
	pinLocal(t)
	m := idleAt(t, 140, 40)
	m.snap.agents["old-kimi"] = &agentState{registered: true, provider: "moonshot", state: "idle",
		stateAt: m.now.Add(-20 * 24 * time.Hour), lastSeen: m.now.Add(-20 * 24 * time.Hour)}
	if got := strings.Join(m.idleData().strip.tokens, " | "); strings.Contains(got, "kimi") {
		t.Errorf("a dormant agent's provider shows: %q", got)
	}
}

// idle-view-project-name (the person): the strip names the project before
// the clock ("agent-bus-monitor · 16:04"), the frames untouched; too narrow,
// the name goes first, then the clock as before.
func TestIdleStripNamesTheProject(t *testing.T) {
	s := stripParts{tokens: []string{"claude 62% 5h"}, planCount: "1/3 33%", clock: "16:04", project: "agent-bus-monitor"}
	if l := idleStripLine(s, 80); !strings.HasSuffix(l.text(), "agent-bus-monitor "+GlyphNone+" 16:04") || l.Width() != 80 {
		t.Errorf("80: %q", l.text())
	}
	left := "claude 62% 5h " + GlyphNone + " 1/3 33%"
	if l := idleStripLine(s, Width(left)+2+Width("16:04")); l.text() != left+"  16:04" {
		t.Errorf("no room for the name: %q", l.text())
	}
	if l := idleStripLine(s, Width(left)); l.text() != left {
		t.Errorf("no room for the clock: %q", l.text())
	}
}

// On screen at every tier the strip ends with the project and the clock, and
// the frames keep their place.
func TestIdleViewShowsTheProject(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{190, 44}, {120, 40}, {80, 24}} {
		m := idleAt(t, size[0], size[1])
		lines := strings.Split(m.grid().plain(), "\n")
		strip := strings.TrimRight(lines[m.height-1], " ")
		if !strings.HasSuffix(strip, m.cfg.project+" "+GlyphNone+" 16:04") {
			t.Errorf("%dx%d: strip %q", size[0], size[1], strip)
		}
	}
}
