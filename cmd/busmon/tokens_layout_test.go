package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// tokens-layout (the person's feedback on the Tokens page): a wider 7-day
// sparkline scaled per row with its period under the header, one-line bars
// with a gap between rows, a header over the totals, account windows named
// in full.

// sparkWide spreads the values over cells: each value repeated so the line
// is cells wide (block, ascii) or 2 points per cell (braille).
func TestSparkWide(t *testing.T) {
	week := []float64{1, 2, 3, 4, 5, 6, 7}
	for _, style := range []string{graphsBlock, graphsASCII, graphsBraille} {
		got := finedraw.SparkWide(week, 7, tokSparkCells, style)
		if Width(got) != tokSparkCells {
			t.Errorf("%s: %d cells, want %d: %q", style, Width(got), tokSparkCells, got)
		}
	}
	if got, want := finedraw.SparkWide(week, 7, 14, graphsBlock), finedraw.Spark([]float64{1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7}, 7, graphsBlock); got != want {
		t.Errorf("block: each day two cells: %q, want %q", got, want)
	}
	if got, want := finedraw.SparkWide(week, 7, 14, graphsBraille), string([]rune(finedraw.Spark(repeatEach(week, 4), 7, graphsBraille))[:14]); got != want {
		t.Errorf("braille: each day four points: %q, want %q", got, want)
	}
	if got := finedraw.SparkWide(nil, 0, 14, graphsBlock); got != strings.Repeat(" ", 14) {
		t.Errorf("no values: %q", got)
	}
}

func repeatEach(vals []float64, n int) []float64 {
	var out []float64
	for _, v := range vals {
		for range n {
			out = append(out, v)
		}
	}
	return out
}

// projectRow is the Projects row of name.
func projectRow(t *testing.T, s, name string) string {
	t.Helper()
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "  "+name+" ") {
			return l
		}
	}
	t.Fatalf("no row for %s:\n%s", name, s)
	return ""
}

// Each row's sparkline is scaled to its own largest day: a quiet project's
// one busy day reaches the top step, though another project's days are larger.
func TestTokensSparkScaledPerRow(t *testing.T) {
	pinLocal(t)
	s := pageText(tokFixtureState(shellNow), nil, 120)
	if row := projectRow(t, s, "blog"); !strings.Contains(row, strings.Repeat(GlyphBarFull, 2)) {
		t.Errorf("blog's only day is not at the top step: %q", row)
	}
}

// The header names the sparkline's column and the totals' column, each over
// its own column, and the line under it says the sparkline's period.
func TestTokensProjectsHeader(t *testing.T) {
	pinLocal(t)
	for _, width := range []int{80, 120, 190} {
		s := pageText(tokFixtureState(shellNow), nil, width)
		lines := strings.Split(s, "\n")
		h := -1
		for i, l := range lines {
			if strings.HasPrefix(l, "Projects") {
				h = i
			}
		}
		if h < 0 || h+2 >= len(lines) {
			t.Fatalf("%d: no Projects header:\n%s", width, s)
		}
		head, period, row := lines[h], lines[h+1], projectRow(t, s, "agent-bus-monitor")
		spark := strings.Index(head, "7 days")
		if spark < 0 || !strings.Contains(head, "7d total") {
			t.Fatalf("%d: header %q lacks its columns", width, head)
		}
		// The total column ends where the header's "7d total" ends.
		if Width(strings.TrimRight(head, " ")) != Width(strings.TrimRight(row, " ")) {
			t.Errorf("%d: 7d total is not over the totals:\n%q\n%q", width, head, row)
		}
		first, today := tokDays(shellNow)
		if !strings.Contains(period, first+" to "+today) || Width(period[:strings.Index(period, first)]) != Width(head[:spark]) {
			t.Errorf("%d: the period is not under the sparkline's header:\n%q\n%q", width, head, period)
		}
	}
}

// tokDays are the first and last of the page's seven days, as MM-DD.
func tokDays(now time.Time) (string, string) {
	d := tokens.LastDays(now, time.Local, 7)
	return d[0][5:], d[len(d)-1][5:]
}

// Bar rows never touch: a blank line between two rows of a zone, so
// full-height blocks never merge into one staircase.
func TestTokensBarRowsHaveAGap(t *testing.T) {
	pinLocal(t)
	agents := map[string]*agentState{
		"coder":    {registered: true, pid: 1, identSession: "0198e5c4-coder-1", provider: "anthropic"},
		"foureyes": {registered: true, pid: 2, identSession: "0198e5c4-four-1", provider: "anthropic"},
	}
	withPalette(t, "dark", false, 1<<24)
	s := pageText(tokFixtureState(shellNow), agents, 120)
	bar := func(l string) bool {
		return strings.Contains(l, GlyphBarFull) || strings.ContainsAny(l, GlyphEighth1+GlyphEighth4+GlyphEighth7+GlyphGauge)
	}
	lines := strings.Split(s, "\n")
	rows := 0
	for i := 1; i < len(lines); i++ {
		if bar(lines[i]) {
			rows++
			if bar(lines[i-1]) {
				t.Errorf("two bar rows touch:\n%s\n%s", lines[i-1], lines[i])
			}
		}
	}
	if rows < 4 {
		t.Errorf("%d bar rows, want the two projects and two agents:\n%s", rows, s)
	}
}

// An account window's name is shown in full, never cut to "7d…".
func TestAccountCardWindowNamesInFull(t *testing.T) {
	pinLocal(t)
	now := shellNow
	b := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{
		{Name: "5h", UsedPct: 10, ResetsAt: now.Unix() + 3600, ObservedAt: now.UnixMilli()},
		{Name: "7d-fable", UsedPct: 40, ResetsAt: now.Unix() + 86400, ObservedAt: now.UnixMilli()}}}
	for _, width := range []int{70, 80} {
		var bars []int
		for _, l := range accountCard(iconsOff, "anthropic", b, nil, now, width, barView{}) {
			txt := l.text()
			if strings.Contains(txt, "…") {
				t.Errorf("%d: a cut label: %q", width, txt)
			}
			if i := strings.Index(txt, GlyphThinBar); i >= 0 && (strings.Contains(txt, " 5h ") || strings.Contains(txt, "7d-fable ")) {
				bars = append(bars, Width(txt[:i]))
			}
		}
		if len(bars) != 2 || bars[0] != bars[1] {
			t.Errorf("%d: the two windows' bars do not start in one column: %v", width, bars)
		}
	}
}

// Below the width the 14-cell sparkline needs, it narrows to one cell per
// day, so the 7d total stays on screen (reviewer-codex on 90286e22): every
// known row keeps its total, at the widths the page is drawn at.
func TestTokensNarrowKeepsTheTotals(t *testing.T) {
	pinLocal(t)
	for width := 60; width <= 100; width++ {
		s := pageText(tokFixtureState(shellNow), nil, width)
		for name, total := range map[string]string{"agent-bus-monitor": "~5.1M", "blog": "170k"} {
			if row := projectRow(t, s, name); !strings.HasSuffix(strings.TrimRight(row, " "), total) {
				t.Errorf("%d: %s's total is cut: %q", width, name, row)
			}
		}
		if !strings.Contains(s, "7d total") {
			t.Errorf("%d: the totals' header is cut:\n%s", width, s)
		}
	}
}
