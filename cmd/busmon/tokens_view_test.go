package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// tokFixtureState is today's shape: this project busy and partial, one
// quieter, one with no tokens key; a 24-hour history for claude's 5h window.
func tokFixtureState(now time.Time) tokState {
	day := func(back int) string { return now.In(time.Local).AddDate(0, 0, -back).Format("2006-01-02") }
	abm := map[string]bus.TokenRow{
		bus.TokenKey(day(0), "foureyes", "anthropic"): {In: 1_000_000, Write: 200_000, Read: 500_000_000, Out: 206_000, N: 40},
		bus.TokenKey(day(0), "coder", "anthropic"):    {In: 300_000, Write: 45_000, Out: 83_000, N: 20},
		bus.TokenKey(day(1), "coder", "anthropic"):    {In: 2_000_000, Out: 500_000, N: 10},
		bus.TokenKey(day(3), "master", "anthropic"):   {In: 700_000, Out: 100_000, N: 5},
	}
	hl := map[string]bus.TokenRow{bus.TokenKey(day(5), "coder", "anthropic"): {In: 170_000, N: 1}}
	st := tokState{polled: true, projects: []tokProject{
		{name: "hyperliquid", known: true, days: daysOf(hl)},
		{name: "agent-bus-monitor", known: true, partial: true, days: daysOf(abm), rows: abm},
		{name: "polymarket"},
	}}
	for h := 0; h < 24; h += 3 {
		st.hist = append(st.hist, bus.QuotaSample{Provider: "anthropic", Window: "5h", Pct: float64(h * 4), At: now.Add(-time.Duration(23-h) * time.Hour).UnixMilli()})
	}
	return st
}

func daysOf(rows map[string]bus.TokenRow) map[string]bus.TokenRow {
	out := map[string]bus.TokenRow{}
	for k, v := range rows {
		d, _, _ := strings.Cut(k, "|")
		s := out[d]
		s.In, s.Write, s.Read, s.Out, s.N = s.In+v.In, s.Write+v.Write, s.Read+v.Read, s.Out+v.Out, s.N+v.N
		out[d] = s
	}
	return out
}

func tokShell(t *testing.T, w, h int) Model {
	t.Helper()
	m := fixtureShell(t, w, h)
	m.snap.tok = tokFixtureState(shellNow)
	m = keys(t, m, 'U')
	if !m.rd.open() || m.rd.top().kind != ovTokens {
		t.Fatal("U opened no tokens page")
	}
	return m
}

// The page at 190, 120 and 80 columns.
func TestTokensPageGoldens(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{190, 50}, {120, 44}, {80, 44}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := tokShell(t, sz[0], sz[1])
			m.paint = newPainter(th)
			checkGolden(t, fmt.Sprintf("tokens-%dx%d.txt", sz[0], sz[1]), m.grid().plain()+"\n")
		})
	}
}

func pageText(st tokState, agents map[string]*agentState, width int) string {
	var out []string
	for _, l := range tokensPage(st, nil, nil, agents, "agent-bus-monitor", shellNow, width) {
		out = append(out, l.text())
	}
	return strings.Join(out, "\n")
}

// Not read yet, unknown and zero are three different things on the page:
// "… not read yet", "?" with its reason, and 0.
func TestTokensPagePendingUnknownZero(t *testing.T) {
	agents := map[string]*agentState{
		"coder":          {registered: true, pid: 1, identSession: "s1", provider: "claude"},
		"reviewer-codex": {registered: true, pid: 2, provider: "openai", client: "codex"},
	}
	if s := pageText(tokState{}, agents, 120); !strings.Contains(s, GlyphEllipsis+" not read yet") || strings.Contains(s, " 0 ") {
		t.Errorf("before the first read:\n%s", s)
	}
	none := tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor"}}}
	if s := pageText(none, agents, 120); !strings.Contains(s, "no data") || !strings.Contains(s, "?") {
		t.Errorf("no tokens key:\n%s", s)
	}
	zero := tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor", known: true}}}
	s := pageText(zero, agents, 120)
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "coder") && !strings.Contains(l, " 0 ") {
			t.Errorf("a proved session with nothing today reads 0: %q", l)
		}
		if strings.Contains(l, "reviewer-codex") && (!strings.Contains(l, "?") || !strings.Contains(l, "Codex session not proved")) {
			t.Errorf("an unknown reads ? with its reason: %q", l)
		}
	}
}

// Bars compare across projects (scaled to the largest day of all); the
// hour sparkline keeps the hour's last sample, a space for an hour without.
func TestTokensSparkRules(t *testing.T) {
	if sparkOf(0, 100) != " " || sparkOf(100, 100) != GlyphBarFull || sparkOf(1, 100) != GlyphSpark1 {
		t.Errorf("spark steps: %q %q %q", sparkOf(0, 100), sparkOf(100, 100), sparkOf(1, 100))
	}
	now := shellNow
	hist := []bus.QuotaSample{
		{Provider: "claude", Window: "5h", Pct: 10, At: now.Add(-90 * time.Minute).UnixMilli()},
		{Provider: "claude", Window: "5h", Pct: 99, At: now.Add(-80 * time.Minute).UnixMilli()}, // same hour, later: wins
		{Provider: "claude", Window: "7d", Pct: 50, At: now.Add(-80 * time.Minute).UnixMilli()},
		{Provider: "claude", Window: "5h", Pct: 50, At: now.Add(-30 * time.Hour).UnixMilli()}, // older than 24 h
	}
	s := []rune(hourSpark(hist, "claude", "5h", now))
	if len(s) != 24 || string(s[22]) != GlyphBarFull || strings.TrimSpace(string(s)) != GlyphBarFull {
		t.Errorf("hour spark %q", string(s))
	}
}

// The page stays inside the reader at every alignment size and hostile name.
func TestTokensPageAlignment(t *testing.T) {
	pinLocal(t)
	for _, sz := range shellAlignSizes {
		m := hostileShell(t, sz[0], sz[1], false)
		if m.layout.Tier == TierTooSmall {
			continue
		}
		st := tokFixtureState(shellNow)
		st.projects = append(st.projects, tokProject{name: "p-" + hostileText, known: true, days: map[string]bus.TokenRow{}})
		m.snap.tok = st
		m = keys(t, m, 'U')
		if !m.rd.open() {
			continue
		}
		g := m.grid()
		r := m.readerRect()
		checkFrameAt(t, func(x, y int) string { return g.at(x, y).text }, fmt.Sprintf("tokens page %dx%d", sz[0], sz[1]), r, true)
		for _, l := range m.rd.top().body {
			if l.Width() > r.W-2 {
				t.Errorf("%dx%d: a body line is %d cells in a %d-cell reader", sz[0], sz[1], l.Width(), r.W-2)
				break
			}
		}
	}
}
