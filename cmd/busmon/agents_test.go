package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestParseCtx(t *testing.T) {
	ok := map[string]int64{"608k": 608000, "608k ctx": 608000, "194.5k": 194500, "1.2M": 1200000,
		"42000": 42000, "0": 0, "12K": 12000, " 50k ": 50000}
	for in, want := range ok {
		if got, good := parseCtx(in); !good || got != want {
			t.Errorf("parseCtx(%q) = %d,%v want %d", in, got, good, want)
		}
	}
	for _, in := range []string{"25%", "", "5h12m", "k", "-3k", "1.2.3k", "ctx", "inf", "Infinity", "NaN", "infk", "1e9"} {
		if _, good := parseCtx(in); good {
			t.Errorf("parseCtx(%q) accepted", in)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1k", 194500: "195k", 608000: "608k",
		999499: "999k", 1200000: "1.2M", 15_000_000: "15.0M"} {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestStateLook(t *testing.T) {
	cases := []struct {
		state, glyph, word string
		role               Role
		group              int
	}{
		{"blocked", GlyphBlocked, "blocked", RoleError, 1},
		{"working", GlyphWorking, "working", RoleOK, 3},
		{"idle", GlyphIdle, "idle", RoleMuted, 4},
		{"done", GlyphDone, "done", RoleDone, 5},
		{"review", GlyphIdle, "review", RoleMuted, 4},
		{"unknown-future", GlyphIdle, "unknown-future", RoleMuted, 4},
		{"", GlyphNone, GlyphDash, RoleMuted, 6},
	}
	for _, c := range cases {
		g, w, r, grp := stateLook(&agentState{state: c.state})
		if g != c.glyph || w != c.word || r != c.role || grp != c.group {
			t.Errorf("%q: %q %q %v %d", c.state, g, w, r, grp)
		}
	}
}

func flagWords(fs []agentFlag) string {
	var w []string
	for _, f := range fs {
		w = append(w, f.Word)
	}
	return strings.Join(w, ",")
}

func TestAgentFlagsTable(t *testing.T) {
	cases := []struct {
		name  string
		a     agentState
		pilot bool
		want  string
		role  Role
	}{
		{"escalated", agentState{wake: &bus.WakeRecord{Escalated: "true"}}, false, "escalated", RoleError},
		{"escalating", agentState{wake: &bus.WakeRecord{Escalated: "pending"}}, false, "escalating", RoleError},
		{"gate", agentState{gated: 2}, false, "gate 2", RoleError},
		{"deferred", agentState{wake: &bus.WakeRecord{State: "deferred"}}, false, "deferred", RoleAttention},
		{"unobserved", agentState{wake: &bus.WakeRecord{State: "unobserved"}}, false, "unobserved", RoleAttention},
		{"waking", agentState{wake: &bus.WakeRecord{State: "prompting"}}, false, "waking", RoleDispatch},
		{"woken", agentState{wake: &bus.WakeRecord{State: "prompted"}}, false, "woken", RoleDispatch},
		{"pilot", agentState{}, true, "pilot", RoleAccent},
		{"armed", agentState{armed: true}, false, "armed", RoleMuted},
		{"stale", agentState{stale: 3}, false, "stale 3", RoleMuted},
		{"unknown wake", agentState{wake: &bus.WakeRecord{State: "weird"}}, false, "wake:weird", RoleMuted},
		{"clear", agentState{wake: &bus.WakeRecord{State: "clear"}}, false, "", 0},
		{"empty", agentState{wake: &bus.WakeRecord{}}, false, "", 0},
	}
	for _, c := range cases {
		fs := agentFlags(&c.a, c.pilot)
		if flagWords(fs) != c.want {
			t.Errorf("%s: flags %q, want %q", c.name, flagWords(fs), c.want)
			continue
		}
		if len(fs) == 1 && fs[0].Role != c.role {
			t.Errorf("%s: role %v, want %v", c.name, fs[0].Role, c.role)
		}
	}
	multi := agentFlags(&agentState{armed: true, gated: 1, wake: &bus.WakeRecord{State: "deferred", Escalated: "true"}}, true)
	if got := flagWords(multi); got != "escalated,gate 1,deferred,pilot,armed" {
		t.Errorf("priority order %q", got)
	}
	// Escalation, wake and gate flags never change the declared state.
	a := &agentState{state: "working", wake: &bus.WakeRecord{Escalated: "true"}}
	if _, w, _, _ := stateLook(a); w != "working" {
		t.Errorf("flags changed the declared state to %q", w)
	}
}

func TestAgentOrder(t *testing.T) {
	m := map[string]*agentState{
		"z-blocked":  {state: "blocked"},
		"a-gated":    {state: "idle", gated: 1},
		"m-escal":    {state: "done", wake: &bus.WakeRecord{Escalated: "pending"}},
		"b-queued":   {state: "idle", queueKnown: true, waiting: 2},
		"c-deferred": {state: "working", wake: &bus.WakeRecord{State: "deferred"}},
		"d-working":  {state: "working"},
		"e-idle":     {state: "idle"},
		"f-review":   {state: "review"},
		"g-done":     {state: "done"},
		"h-none":     {},
		// An unmeasured or failed queue never moves an agent between groups.
		"i-unknownq": {state: "idle", waiting: 5},
		"j-failedq":  {state: "done", queueKnown: true, queueFailed: true, waiting: 4},
		"k-pilot":    {state: "idle"},
	}
	got := sortedAgentNames(m, "k-pilot")
	want := []string{"a-gated", "m-escal", "z-blocked", "b-queued", "c-deferred", "d-working",
		"e-idle", "f-review", "i-unknownq", "k-pilot", "g-done", "j-failedq", "h-none"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
}

func TestQueueCellStates(t *testing.T) {
	cases := []struct {
		a    agentState
		tier Tier
		text string
		role Role
	}{
		{agentState{}, TierWide, GlyphDash, RoleMuted},
		{agentState{queueKnown: true}, TierWide, GlyphNone, RoleDim},
		{agentState{queueKnown: true, waiting: 2}, TierWide, "2 msg", RoleAttention},
		{agentState{queueKnown: true, waiting: 2}, TierMedium, "2", RoleAttention},
		{agentState{queueKnown: true, queueFailed: true, waiting: 2}, TierWide, "2?", RoleMuted},
		{agentState{queueKnown: false, queueFailed: true}, TierWide, GlyphDash, RoleMuted},
	}
	for i, c := range cases {
		s := queueCell(&c.a, c.tier)
		if s.Text != c.text || s.Role != c.role {
			t.Errorf("case %d: %q %v, want %q %v", i, s.Text, s.Role, c.text, c.role)
		}
	}
}

func hostileAgent(now time.Time) *agentState {
	return &agentState{state: "blocked", message: "日本語👩‍💻\tneeds\nreview\x1b[31m [red]x[-]",
		stateAt: now.Add(-6 * time.Minute), queueKnown: true, waiting: 12, ctxRaw: "608k ctx",
		gated: 1, armed: true, wake: &bus.WakeRecord{State: "deferred"}}
}

func TestAgentRowWidth(t *testing.T) {
	now := time.Now()
	name := strings.Repeat("n", 32)
	for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
		for w := 1; w <= 200; w++ {
			for _, a := range []*agentState{hostileAgent(now), {}} {
				if got := agentRow(name, a, true, tier, w, now).Width(); got != w {
					t.Fatalf("tier %d width %d: row %d cells", tier, w, got)
				}
			}
			if h := agentHeader(tier, w); tier == TierWide && h.Width() != w {
				t.Fatalf("wide header %d at %d", h.Width(), w)
			} else if tier != TierWide && h != nil {
				t.Fatalf("tier %d has a header", tier)
			}
		}
	}
	row := agentRow("grok", hostileAgent(now), false, TierWide, 160, now).text()
	for _, want := range []string{GlyphBlocked + " grok", "blocked", "6m", "12 msg", "608k", "gate 1", "deferred", "needs review"} {
		if !strings.Contains(row, want) {
			t.Errorf("wide row lacks %q: %q", want, row)
		}
	}
	for _, bad := range []string{"\t", "\n", "\x1b"} {
		if strings.Contains(row, bad) {
			t.Errorf("wide row keeps %q", bad)
		}
	}
	nar := agentRow("grok", hostileAgent(now), false, TierNarrow, 40, now).text()
	if !strings.Contains(nar, "gate 1 日") {
		t.Errorf("narrow row must fold the flag into the message: %q", nar)
	}
	med := agentRow("grok", hostileAgent(now), false, TierMedium, 100, now).text()
	if strings.Contains(med, "608k") || strings.Contains(med, GlyphBarFull) || !strings.Contains(med, "gate 1") || strings.Contains(med, "deferred") {
		t.Errorf("medium row must drop bar and ctx and keep one flag: %q", med)
	}
}

func TestUpdatedBar(t *testing.T) {
	now := time.Now()
	cases := []struct {
		at   time.Time
		full int
		age  string
	}{
		{now, 10, "0s"},
		{now.Add(-5 * time.Minute), 5, "5m"},
		{now.Add(-10 * time.Minute), 0, "10m"},
		{now.Add(-3 * time.Hour), 0, "3h"},
		{now.Add(time.Minute), 10, "0s"}, // clock skew: a declaration in the future
	}
	for _, c := range cases {
		segs := updatedCell(&agentState{state: "idle", stateAt: c.at}, TierWide, now)
		text := Line(segs).text()
		if n := strings.Count(text, GlyphBarFull); n != c.full {
			t.Errorf("at %v: %d full cells in %q, want %d", now.Sub(c.at), n, text, c.full)
		}
		if !strings.Contains(text, c.age) {
			t.Errorf("at %v: %q lacks age %q", now.Sub(c.at), text, c.age)
		}
	}
	none := Line(updatedCell(&agentState{}, TierWide, now)).text()
	if !strings.Contains(none, strings.Repeat(GlyphNone, 10)) || !strings.Contains(none, GlyphDash) {
		t.Errorf("no declaration: %q", none)
	}
	if strings.Contains(Line(updatedCell(&agentState{stateAt: now}, TierMedium, now)).text(), GlyphBarFull) {
		t.Error("medium must not draw the bar")
	}
}
