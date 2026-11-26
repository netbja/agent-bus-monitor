package main

import (
	"fmt"
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

// tokStylePage is the Tokens page's text in graphs style at width.
func tokStylePage(st tokState, graphs string, width int) []Line {
	return tokensPage(iconsOff, graphs, st, nil, nil, nil, "agent-bus-monitor", shellNow, width, false, barView{}, true)
}

// tui-rendering §4.2: in fine the 7-day sparkline is two braille rows, its
// top row on the line above the project's, 28 cells where the row has room
// and 14 otherwise; the row's largest day is printed beside its top row.
// Block keeps one row (the line above holds only the largest day).
func TestTokensTwoRowSparkline(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", false, 1<<24)
	for _, c := range []struct {
		graphs string
		width  int
		cells  int
		rows   int
	}{{graphsBraille, 120, tokSparkWide, 2}, {graphsBraille, 100, tokSparkCells, 2}, {graphsBlock, 120, tokSparkCells, 1}} {
		lines := tokStylePage(tokFixtureState(shellNow), c.graphs, c.width)
		at := -1
		for i, l := range lines {
			if strings.HasPrefix(l.text(), "  blog ") {
				at = i
			}
		}
		if at < 1 {
			t.Fatalf("%s %d: no blog row", c.graphs, c.width)
		}
		row, above := lines[at].text(), lines[at-1].text()
		col := 22 + Width(fmtCols(c.width)) + 2
		spark := string([]rune(row)[col : col+c.cells])
		if !strings.Contains(above, "max 170k/day") {
			t.Errorf("%s %d: no max beside it: %q", c.graphs, c.width, above)
		}
		top := string([]rune(above)[col : col+c.cells])
		if c.rows == 2 {
			// blog's one day is its largest: full on both rows, 4 cells a day at 28.
			if strings.Count(spark, "⣿") != c.cells/7 || strings.Count(top, "⣿") != c.cells/7 {
				t.Errorf("%s %d: rows %q / %q", c.graphs, c.width, top, spark)
			}
		} else if strings.TrimSpace(top) != "" || !strings.Contains(spark, GlyphBarFull) {
			t.Errorf("%s %d: one row: %q / %q", c.graphs, c.width, top, spark)
		}
	}
}

func fmtCols(width int) string {
	if width < 90 {
		return " " + strings.Repeat(" ", 8)
	}
	return strings.Repeat(" ", 18)
}

// tui-rendering §4.2: a project with no tokens in the seven days is dimmed;
// more than three fold into one line. The open project is never folded.
func TestTokensIdleProjects(t *testing.T) {
	pinLocal(t)
	st := tokFixtureState(shellNow)
	idle := func(n int) tokState {
		s := st
		s.projects = append([]tokProject(nil), st.projects...)
		for i := range n {
			s.projects = append(s.projects, tokProject{name: fmt.Sprintf("quiet%d", i), known: true, days: map[string]bus.TokenRow{}})
		}
		return s
	}
	find := func(lines []Line, prefix string) (Line, bool) {
		for _, l := range lines {
			if strings.HasPrefix(l.text(), prefix) {
				return l, true
			}
		}
		return nil, false
	}
	lines := tokStylePage(idle(3), graphsBraille, 120)
	for i := range 3 {
		l, ok := find(lines, fmt.Sprintf("  quiet%d ", i))
		if !ok {
			t.Fatalf("three idle: quiet%d folded", i)
		}
		for _, s := range l {
			if strings.TrimSpace(s.Text) != "" && strings.ContainsAny(s.Text, "abcdefghijklmnopqrstuvwxyz0123456789") && s.Role != RoleDim {
				t.Errorf("quiet%d: %q in %v, not dimmed", i, s.Text, s.Role)
			}
		}
	}
	for i, l := range lines { // an idle row has no largest day to print
		if strings.HasPrefix(l.text(), "  quiet0 ") && strings.Contains(lines[i-1].text(), "max ") {
			t.Errorf("idle row with a max: %q", lines[i-1].text())
		}
	}
	if _, ok := find(lines, "  3 projects idle"); ok {
		t.Error("three idle fold")
	}
	lines = tokStylePage(idle(4), graphsBraille, 120)
	if _, ok := find(lines, "  quiet0 "); ok {
		t.Error("four idle: quiet0 still listed")
	}
	if l, ok := find(lines, "  4 projects idle this week"); !ok || l[len(l)-1].Role != RoleDim && l[1].Role != RoleDim {
		t.Errorf("four idle: no fold line")
	}
	// The open project, idle, is never folded nor dimmed.
	s := idle(4)
	for i, p := range s.projects {
		if p.name == "agent-bus-monitor" {
			s.projects[i].days = map[string]bus.TokenRow{}
		}
	}
	if l, ok := find(tokStylePage(s, graphsBraille, 120), "  agent-bus-monitor "); !ok || l[1].Role != RoleAccent {
		t.Errorf("the open project idle: %v %+v", ok, l)
	}
}
