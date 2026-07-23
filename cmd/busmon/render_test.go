package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestStatusBar(t *testing.T) {
	master := statusBar("trading", "hermes", nil)
	if !strings.Contains(master, "⬢ MASTER hermes") {
		t.Fatalf("statusBar(driver) = %q, want '⬢ MASTER hermes'", master)
	}
	if !strings.Contains(master, "trading") {
		t.Fatalf("statusBar = %q, want the project name", master)
	}
	auto := statusBar("trading", "", nil)
	if !strings.Contains(auto, "autonomous") || strings.Contains(auto, "MASTER") {
		t.Fatalf("statusBar(\"\") = %q, want 'autonomous' and no MASTER", auto)
	}
}

func TestEntryTime(t *testing.T) {
	if got, want := entryTime("1779707136877-0"), time.UnixMilli(1779707136877); !got.Equal(want) {
		t.Fatalf("entryTime = %v, want %v", got, want)
	}
	if entryTime("bogus").IsZero() {
		t.Fatal("entryTime(bogus) returned zero time, want a fallback")
	}
}

func TestSelPos(t *testing.T) {
	feed := []feedLine{{id: "1"}, {id: "2"}, {id: "3"}}
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"1", 0},
		{"3", 2},
		{"99", -1}, // scrolled out of the capped feed
		{"", -1},   // live mode: no selection
	} {
		if got := selPos(feed, tc.id); got != tc.want {
			t.Errorf("selPos(feed, %q) = %d, want %d", tc.id, got, tc.want)
		}
	}
	if got := selPos(nil, "1"); got != -1 {
		t.Errorf("selPos(nil, _) = %d, want -1", got)
	}
}

func TestSelectionTitle(t *testing.T) {
	got := selectionTitle(3, 42)
	if !strings.Contains(got, "3/42") {
		t.Errorf("selectionTitle(3,42) = %q, want it to show 3/42", got)
	}
	if !strings.Contains(got, "copy") {
		t.Errorf("selectionTitle = %q, want it to mention the copy key", got)
	}
}

func TestAgentLabel(t *testing.T) {
	now := time.Now()

	base := &agentState{state: "working", lastSeen: now}
	if got := agentLabel("dev", base, now, false); !strings.Contains(got, "dev: working") {
		t.Fatalf("agentLabel base = %q, want it to show 'dev: working'", got)
	}
	if strings.Contains(agentLabel("dev", base, now, false), "👂") {
		t.Fatal("unarmed agent should not show the listening badge")
	}

	armed := &agentState{state: "idle", lastSeen: now, armed: true}
	if got := agentLabel("dev", armed, now, false); !strings.Contains(got, "👂") {
		t.Fatalf("armed agent label = %q, want a 👂 badge", got)
	}

	// Backlog while listening → yellow ⌛ (normal/transient).
	busy := &agentState{state: "idle", lastSeen: now, armed: true, lag: 2}
	if got := agentLabel("dev", busy, now, false); !strings.Contains(got, "⌛2") || !strings.Contains(got, "[yellow]") {
		t.Fatalf("armed+lag label = %q, want a yellow ⌛2", got)
	}

	// Backlog with nobody listening → the "stopped listening" tell, orange ⌛.
	dead := &agentState{state: "idle", lastSeen: now, armed: false, lag: 5}
	if got := agentLabel("dev", dead, now, false); !strings.Contains(got, "⌛5") || !strings.Contains(got, "[orange]") {
		t.Fatalf("unarmed+lag label = %q, want an orange ⌛5 warning", got)
	}

	gated := &agentState{state: "working", lastSeen: now, gated: 1}
	if got := agentLabel("dev", gated, now, false); !strings.Contains(got, "🔒1") {
		t.Fatalf("gated agent label = %q, want a 🔒1 badge", got)
	}

	attached := &agentState{state: "working", lastSeen: now, pane: "w1:p1"}
	if got := agentLabel("dev", attached, now, false); !strings.Contains(got, "⧉") {
		t.Fatalf("herdr-attached agent label = %q, want a ⧉ badge", got)
	}
	if strings.Contains(agentLabel("dev", base, now, false), "⧉") {
		t.Fatal("agent with no pane should not show the ⧉ badge")
	}
}

func TestAgentLabelMaster(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "working", lastSeen: now}
	if got := agentLabel("hermes", a, now, true); !strings.Contains(got, "⬢") {
		t.Fatalf("master label = %q, want a ⬢ marker", got)
	}
	if got := agentLabel("hermes", a, now, false); strings.Contains(got, "⬢") {
		t.Fatalf("non-master label = %q, should not have ⬢", got)
	}
}

func TestParseDirected(t *testing.T) {
	if tgt, body, ok := parseDirected("@claude1 do the thing"); !ok || tgt != "claude1" || body != "do the thing" {
		t.Fatalf("parseDirected directed = (%q,%q,%v), want claude1/do the thing/true", tgt, body, ok)
	}
	if _, body, ok := parseDirected("hello world"); ok || body != "hello world" {
		t.Fatalf("parseDirected plain = (_,%q,%v), want (hello world,false)", body, ok)
	}
	if _, _, ok := parseDirected("@claude1"); ok {
		t.Fatal("parseDirected with no body should not be directed")
	}
	if _, _, ok := parseDirected("@Bad foo"); ok {
		t.Fatal("parseDirected with an invalid agent name should fall back (not directed)")
	}
}

func TestAgentCompletions(t *testing.T) {
	names := []string{"claude2", "claude1", "hermes"}
	got := agentCompletions("@cl", names)
	if len(got) != 2 || got[0] != "@claude1 " || got[1] != "@claude2 " {
		t.Fatalf("agentCompletions(@cl) = %v, want [@claude1 , @claude2 ]", got)
	}
	if agentCompletions("@claude1 do", names) != nil {
		t.Fatal("no completions once the body has started (space present)")
	}
	if agentCompletions("hello", names) != nil {
		t.Fatal("no completions when not @-prefixed")
	}
}

func TestUsageBadge(t *testing.T) {
	// The chip badge is the agent's OWN context fill; account-scope numbers are
	// identical for every agent and live in the status bar instead.
	if got := usageBadge(bus.UsageSnapshot{Ctx: "608k ctx", Model: "claude-sonnet-5"}); got != "608k ctx" {
		t.Fatalf("ctx = %q, want 608k ctx", got)
	}
	// Ctx wins over the legacy account-scope strings when both are present.
	if got := usageBadge(bus.UsageSnapshot{Ctx: "42k ctx", Session: "99%"}); got != "42k ctx" {
		t.Fatalf("ctx should win = %q, want 42k ctx", got)
	}
	// Legacy fallback: a snapshot from the old status-line tee still renders.
	if got := usageBadge(bus.UsageSnapshot{Session: "99%", Reset: "36m"}); got != "99%·36m" {
		t.Fatalf("both = %q, want 99%%·36m", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Session: "99%"}); got != "99%" {
		t.Fatalf("session only = %q, want 99%%", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Reset: "36m"}); got != "36m" {
		t.Fatalf("reset only = %q, want 36m", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Model: "Opus"}); got != "" {
		t.Fatalf("neither = %q, want empty", got)
	}
}

func TestAgentLabelUsage(t *testing.T) {
	now := time.Now()
	withUsage := &agentState{state: "working", lastSeen: now, usage: "99%·36m"}
	if got := agentLabel("dev", withUsage, now, false); !strings.Contains(got, "99%·36m") {
		t.Fatalf("agentLabel with usage = %q, want the usage badge", got)
	}
	noUsage := &agentState{state: "working", lastSeen: now}
	if strings.Contains(agentLabel("dev", noUsage, now, false), "[gray][") {
		t.Fatal("no usage badge when usage is empty")
	}
}

func TestReportMarker(t *testing.T) {
	if got := reportMarker(""); got != "" {
		t.Fatalf("empty full → no marker, got %q", got)
	}
	if got := reportMarker("the\nfull\ntext"); got != " (+13)" { // 13 runes
		t.Fatalf("marker = %q, want ' (+13)'", got)
	}
}

func TestPackChips(t *testing.T) {
	// (a) all chips fit on one row
	rows, used := packChips([]chip{{"a", 1}, {"b", 1}, {"c", 1}}, 100, 4)
	if used != 1 || len(rows) != 1 {
		t.Fatalf("(a) used=%d rows=%v, want 1 row", used, rows)
	}
	if !strings.Contains(rows[0], "a") || !strings.Contains(rows[0], "c") {
		t.Fatalf("(a) row missing chips: %q", rows[0])
	}
	// (b) wrap onto a second row when too wide (w=6 each, sep 2, width 10 → 14 > 10)
	_, used = packChips([]chip{{"AAAAAA", 6}, {"BBBBBB", 6}}, 10, 4)
	if used != 2 {
		t.Fatalf("(b) used=%d, want 2 rows", used)
	}
	// (c) overflow past maxRows → "+N" on the last row
	chips := []chip{{"a", 6}, {"b", 6}, {"c", 6}, {"d", 6}, {"e", 6}}
	rows, used = packChips(chips, 6, 2)
	if used != 2 {
		t.Fatalf("(c) used=%d, want 2 (capped)", used)
	}
	if !strings.Contains(rows[1], "+3") {
		t.Fatalf("(c) last row = %q, want a +3 overflow marker", rows[1])
	}
	// (d) packs by visible width w, not byte length (tagged chip is 10 bytes, w=1)
	tagged := chip{"[green]x[-]", 1}
	_, used = packChips([]chip{tagged, tagged, tagged}, 7, 4) // 1+2+1+2+1 = 7 fits
	if used != 1 {
		t.Fatalf("(d) used=%d, want 1 row (packed by visible width)", used)
	}
}

func TestBudgetBar(t *testing.T) {
	// Nothing published -> nothing rendered. An unknown budget must look
	// unknown, never like 0% used.
	if got := budgetBar(nil); got != "" {
		t.Fatalf("empty budgets = %q, want empty", got)
	}
	one := budgetBar(map[string]bus.BudgetSnapshot{
		"anthropic": {SessionPct: 25, WeeklyPct: 44},
	})
	if !strings.Contains(one, "anthropic 25%/44%") {
		t.Fatalf("budgetBar = %q, want 'anthropic 25%%/44%%'", one)
	}
	// Sorted, so the bar doesn't reshuffle every tick.
	two := budgetBar(map[string]bus.BudgetSnapshot{
		"openai":    {SessionPct: 1, WeeklyPct: 2},
		"anthropic": {SessionPct: 3, WeeklyPct: 4},
	})
	if strings.Index(two, "anthropic") > strings.Index(two, "openai") {
		t.Fatalf("providers not sorted: %q", two)
	}
}

func TestBudgetColorWarnsBeforeTheWall(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{{0, "green"}, {74, "green"}, {75, "yellow"}, {89, "yellow"}, {90, "red"}, {100, "red"}}
	for _, c := range cases {
		if got := budgetColor(c.pct); got != c.want {
			t.Errorf("budgetColor(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

// The status bar carries the account budget; a chip never does.
func TestStatusBarShowsBudget(t *testing.T) {
	got := statusBar("trading", "master", map[string]bus.BudgetSnapshot{
		"anthropic": {SessionPct: 25, WeeklyPct: 44},
	})
	if !strings.Contains(got, "anthropic 25%/44%") {
		t.Fatalf("statusBar = %q, want the account budget", got)
	}
	if !strings.Contains(got, "⬢ MASTER master") {
		t.Fatalf("statusBar = %q, want the master indicator kept", got)
	}
}
