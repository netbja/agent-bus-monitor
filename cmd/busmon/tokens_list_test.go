package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func tokPage(all bool, width int) []Line {
	agents := map[string]*agentState{}
	for _, n := range []string{"foureyes", "coder", "architect", "docs", "master", "qa", "release", "security", "sentinel"} {
		agents[n] = &agentState{registered: true, provider: "anthropic"}
	}
	agents["foureyes"].pid, agents["foureyes"].identSession = 1, "s-foureyes"
	agents["coder"].pid, agents["coder"].identSession = 2, "s-coder"
	return tokensPage(tokFixtureState(shellNow), nil, nil, agents, "agent-bus-monitor", shellNow, width, false, barView{}, all)
}

func tokLinesText(ls []Line) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.text() + "\n")
	}
	return b.String()
}

func boldText(l Line) string {
	var b strings.Builder
	for _, s := range l {
		if s.Bold {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// The U page in the list pattern (busmon pages, Tokens): three zones with
// short names, bars against the zone's largest row, its top consumer bold,
// the agents with no proved session folded into one grey line that a lists.
func TestTokensPageInTheListPattern(t *testing.T) {
	text := tokLinesText(tokPage(false, 136))
	for _, w := range []string{"Accounts", "Projects", "Agents, agent-bus-monitor", "7 more " + GlyphNone + " no proved session  (a lists them)",
		"in = input + cache writes " + GlyphNone + " out = output " + GlyphNone + " ~ still counting " + GlyphNone + " no cost, by rule"} {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q:\n%s", w, text)
		}
	}
	for _, gone := range []string{"Account windows", "CACHE RD", "no session proved with its process", "architect"} {
		if strings.Contains(text, gone) {
			t.Errorf("still drawn: %q", gone)
		}
	}
	var abm, blog, foureyes, coder Line
	for _, l := range tokPage(false, 136) {
		switch s := l.text(); {
		case strings.Contains(s, "agent-bus-monitor ") && strings.Contains(s, "1.5M"):
			abm = l
		case strings.HasPrefix(strings.TrimSpace(s), "blog"):
			blog = l
		case strings.HasPrefix(strings.TrimSpace(s), "foureyes"):
			foureyes = l
		case strings.HasPrefix(strings.TrimSpace(s), "coder"):
			coder = l
		}
	}
	if !strings.Contains(boldText(abm), "1.5M") || boldText(blog) != "" {
		t.Errorf("the top project is not the one in bold: %q / %q", boldText(abm), boldText(blog))
	}
	if !strings.Contains(boldText(foureyes), "1.2M") || boldText(coder) != "" {
		t.Errorf("the top agent is not the one in bold: %q / %q", boldText(foureyes), boldText(coder))
	}
	if !strings.Contains(abm.text(), GlyphBarFull) || strings.Count(blog.text(), GlyphBarFull) >= strings.Count(abm.text(), GlyphBarFull) {
		t.Errorf("bars are not against the zone's largest row:\n%s\n%s", abm.text(), blog.text())
	}
	all := tokLinesText(tokPage(true, 136))
	if !strings.Contains(all, "architect") || !strings.Contains(all, "no proved session") || strings.Contains(all, "(a lists them)") {
		t.Errorf("a does not list the folded agents:\n%s", all)
	}
	for _, l := range tokPage(false, 66) {
		if l.Width() > 66 {
			t.Errorf("narrow line of %d cells: %q", l.Width(), l.text())
		}
	}
}

// The title is a counter summary: the period, the fullest account window, the
// held providers.
func TestTokensTitle(t *testing.T) {
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 62, ObservedAt: shellNow.UnixMilli()}, {Name: "7d", UsedPct: 41, ObservedAt: shellNow.UnixMilli()}}},
		"xai":       {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 100, ObservedAt: shellNow.UnixMilli()}}},
	}
	holds := map[string]int64{"xai": shellNow.Add(40 * time.Minute).Unix()}
	got := tokensTitle(budgets, holds, shellNow)
	for _, w := range []string{"Tokens", "today", "anthropic 62% 5h", "xai held 40m"} {
		if !strings.Contains(got, w) {
			t.Errorf("title %q lacks %q", got, w)
		}
	}
}
