package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A row padded after a bar keeps the bar's colour but never its track: the
// track stops at the bar's own width (the U page's Agents zone).
func TestFitLinePaddingDropsTheTrack(t *testing.T) {
	l := FitLine(Line{S(RoleText, "coder "), Seg{Text: "  ", Role: RoleOK, Track: true}}, 20)
	if pad := l[len(l)-1]; pad.Track || strings.TrimSpace(pad.Text) != "" {
		t.Fatalf("padding %+v", pad)
	}
	if l.Width() != 20 {
		t.Fatalf("width %d", l.Width())
	}
}

func tokAccounts(width int, holds map[string]int64) []string {
	at := shellNow.UnixMilli()
	w := func(n string, pct float64, reset int64) bus.BudgetWindow {
		return bus.BudgetWindow{Name: n, UsedPct: pct, ObservedAt: at, ResetsAt: shellNow.Unix() + reset}
	}
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{w("5h", 2, 4*3600), w("7d", 55, 43*3600)}},
		"openai":    {Windows: []bus.BudgetWindow{w("5h", 5, 4*3600), w("7d", 23, 61*3600)}},
		"xai":       {Windows: []bus.BudgetWindow{w("weekly", 34, 48*3600)}},
	}
	var out []string
	for _, l := range tokensPage(tokFixtureState(shellNow), budgets, holds, nil, "agent-bus-monitor", shellNow, width, false, barView{}, false) {
		if l.Width() > width {
			out = append(out, "TOO WIDE: "+l.text())
		}
		s := l.text()
		if s == "Projects" || strings.HasPrefix(s, "Projects ") {
			break
		}
		out = append(out, s)
	}
	return out
}

// Accounts: one row per provider, its windows side by side, each window in
// its own column so the 7d windows line up; the richest form that fits.
func TestTokensAccountsOneRowPerProvider(t *testing.T) {
	for _, c := range []struct {
		width        int
		spark, reset bool
	}{{190, true, true}, {120, false, true}, {80, false, false}, {66, false, false}} {
		rows := tokAccounts(c.width, nil)
		col := -1
		for _, p := range []string{"anthropic", "openai"} {
			var mine []string
			for _, r := range rows {
				if strings.HasPrefix(strings.TrimSpace(r), p) {
					mine = append(mine, r)
				}
			}
			if len(mine) != 1 || !strings.Contains(mine[0], "5h") || !strings.Contains(mine[0], "7d") {
				t.Fatalf("%d: %s rows %q", c.width, p, mine)
			}
			at := Width(mine[0][:strings.Index(mine[0], "7d")]) // in cells: a spark glyph is several bytes
			if col >= 0 && at != col {
				t.Errorf("%d: 7d at %d for %s, %d above", c.width, at, p, col)
			}
			col = at
			if strings.Contains(mine[0], "24h") != c.spark || strings.Contains(mine[0], "resets") != c.reset {
				t.Errorf("%d: spark %v reset %v: %q", c.width, c.spark, c.reset, mine[0])
			}
		}
		for _, r := range rows {
			if strings.HasPrefix(r, "TOO WIDE") {
				t.Errorf("%d: %s", c.width, r)
			}
		}
	}
}

// A hold is never dropped for room: the bare form says it once at the end of
// the provider's row.
func TestTokensAccountsKeepAHold(t *testing.T) {
	holds := map[string]int64{"openai": shellNow.Add(40 * time.Minute).Unix()}
	for _, width := range []int{190, 120, 80, 66} {
		var row string
		for _, r := range tokAccounts(width, holds) {
			if strings.HasPrefix(strings.TrimSpace(r), "openai") {
				row = r
			}
		}
		if !strings.Contains(row, "held 40m") {
			t.Errorf("%d: no hold in %q", width, row)
		}
	}
}

// On screen, a partial agent bar's track ends with the bar: no cell after it, up to
// the reader's border, is drawn on the track.
func TestTokensAgentBarTrackStopsAtTheBar(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", false, 1<<24)
	m := tokShell(t, 190, 50)
	g := m.grid()
	found := false
	for y := 0; y < g.h; y++ {
		row := g.rowText(y)
		if !strings.Contains(row, "│   coder ") {
			continue
		}
		found = true
		last := -1
		for x := 0; x < g.w; x++ {
			if g.at(x, y).st.Track {
				last = x
			}
		}
		if last < 0 || last > 2+16+1+10+18+2+20+1 {
			t.Errorf("track runs to cell %d of %d: %q", last, g.w, row)
		}
	}
	if !found {
		t.Fatal("no coder row on the page")
	}
}
