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
		"openai":    {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 5, ObservedAt: at}, w("7d", 23, 61*3600)}}, // no reset: a shorter cell, padded to its column
		"xai":       {Windows: []bus.BudgetWindow{w("weekly", 34, 48*3600)}},
	}
	var out []string
	for _, l := range tokensPage(iconsOff, tokFixtureState(shellNow), budgets, holds, nil, "agent-bus-monitor", shellNow, width, false, barView{}, false) {
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

// cardOf is a provider's card in the Accounts rows: its head line (the name
// upper case) and the lines under it, up to the rule or the next card.
func cardOf(rows []string, provider string) []string {
	var out []string
	for _, r := range rows {
		t := strings.TrimSpace(r)
		switch {
		case strings.HasPrefix(t, strings.ToUpper(providerLabel(provider))+" "):
			out = []string{r}
		case len(out) > 0 && (strings.HasPrefix(t, glyphHorizontal) || t == "" || t == strings.ToUpper(t) && !strings.ContainsAny(t[:1], "0123456789")):
			return out
		case len(out) > 0:
			out = append(out, r)
		}
	}
	return out
}

// Accounts as cards (usage-cards): one card per provider, one row per window,
// the bars of a card starting in one column, nothing wider than the page and
// no 24-hour spark, at every width.
func TestTokensAccountsCards(t *testing.T) {
	for _, width := range []int{190, 120, 80, 66, 50} {
		rows := tokAccounts(width, nil)
		for _, r := range rows {
			if strings.HasPrefix(r, "TOO WIDE") {
				t.Errorf("%d: %s", width, r)
			}
			if strings.Contains(r, "24h") {
				t.Errorf("%d: a spark is drawn: %q", width, r)
			}
		}
		card := cardOf(rows, "anthropic")
		if len(card) < 3 || !strings.Contains(card[1], "5h") || !strings.Contains(card[2], "7d") {
			t.Fatalf("%d: anthropic card %q", width, card)
		}
		bar := func(r string) int { return Width(r[:strings.IndexAny(r, GlyphThinBar+GlyphThinMark+glyphVertical)]) }
		if bar(card[1]) != bar(card[2]) {
			t.Errorf("%d: the bars start at %d and %d", width, bar(card[1]), bar(card[2]))
		}
		if !strings.Contains(card[1], "2%") || !strings.Contains(card[2], "55%") {
			t.Errorf("%d: figures %q", width, card)
		}
	}
}

// A hold is never dropped for room: the card's head says it.
func TestTokensAccountsKeepAHold(t *testing.T) {
	holds := map[string]int64{"openai": shellNow.Add(40 * time.Minute).Unix()}
	want := "held until " + shellNow.Add(40*time.Minute).Local().Format("15:04")
	for _, width := range []int{190, 120, 80, 66} {
		card := cardOf(tokAccounts(width, holds), "openai")
		if len(card) == 0 || !strings.Contains(card[0], want) {
			t.Errorf("%d: no hold in %q", width, card)
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

// A window named in words gets a short label of its own for the 3-cell name
// column (xai's weekly read "we…"): wk, mo, day; 5h and 7d unchanged.
func TestTokensAccountsShortWindowLabels(t *testing.T) {
	for _, width := range []int{190, 80, 50} {
		card := cardOf(tokAccounts(width, nil), "xai")
		if len(card) < 2 || !strings.HasPrefix(strings.TrimSpace(card[1]), "wk ") || strings.Contains(card[1], "we"+GlyphEllipsis) {
			t.Errorf("%d: xai card %q", width, card)
		}
	}
	for name, want := range map[string]string{"weekly": "wk", "monthly": "mo", "daily": "day", "5h": "5h", "7d": "7d", "spend-monthly": "spend-monthly"} {
		if got := accountWindowName(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
