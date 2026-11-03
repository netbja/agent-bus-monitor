package main

import (
	"context"
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
		{name: "blog", known: true, days: daysOf(hl)},
		{name: "agent-bus-monitor", known: true, partial: true, days: daysOf(abm), rows: abm},
		{name: "wiki"},
	}}
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
	for _, l := range tokensPage(iconsOff, st, nil, nil, agents, "agent-bus-monitor", shellNow, width, false, barView{}, true) { // a: every agent listed
		out = append(out, l.text())
	}
	return strings.Join(out, "\n")
}

// Not read yet, unknown and zero are three different things on the page:
// "… not read yet", "?" with its reason, and 0.
func TestTokensPagePendingUnknownZero(t *testing.T) {
	agents := map[string]*agentState{
		"coder":          {registered: true, pid: 1, identSession: "0198e5c4-coder-1", provider: "claude"},
		"reviewer-codex": {registered: true, pid: 2, provider: "openai", client: "codex"},
	}
	// The open project's row stays, unknown, with its reason: never folded, never 0.
	openRow := func(s string) string {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "agent-bus-monitor ") && strings.Contains(l, "?") {
				return l
			}
		}
		return ""
	}
	if s := pageText(tokState{}, agents, 120); !strings.Contains(openRow(s), "not read yet") || strings.Contains(s, " 0 ") {
		t.Errorf("before the first read:\n%s", s)
	}
	none := tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor"}, {name: "other"}}}
	s := pageText(none, agents, 120)
	if !strings.Contains(openRow(s), "no tokens recorded") || strings.Contains(s, "agent-bus-monitor other") {
		t.Errorf("no tokens key: the open project must be its own ? row, the others fold:\n%s", s)
	}
	if !strings.Contains(s, "other  no data") {
		t.Errorf("another project without a key no longer folds:\n%s", s)
	}
	zero := tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor", known: true}}}
	s = pageText(zero, agents, 120)
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
func TestTokensSparkSteps(t *testing.T) {
	if sparkOf(0, 100) != " " || sparkOf(100, 100) != GlyphBarFull || sparkOf(1, 100) != GlyphSpark1 {
		t.Errorf("spark steps: %q %q %q", sparkOf(0, 100), sparkOf(100, 100), sparkOf(1, 100))
	}
}

// The page stays inside the reader at every alignment size and hostile name.
func TestTokensPageAlignment(t *testing.T) {
	pinLocal(t)
	for _, icons := range []string{iconsOff, iconsNerd, iconsFont} { // busmon-icons §5: every mode
		for _, sz := range shellAlignSizes {
			m := hostileShell(t, sz[0], sz[1], false)
			if m.layout.Tier == TierTooSmall {
				continue
			}
			m.cfg.settings.Icons = icons
			withIconTeam(m.snap.agents)
			st := tokFixtureState(shellNow)
			st.projects = append(st.projects, tokProject{name: "p-" + hostileText, known: true, days: map[string]bus.TokenRow{}})
			m.snap.tok = st
			m = keys(t, m, 'U')
			if !m.rd.open() {
				continue
			}
			g := m.grid()
			r := m.readerRect()
			checkFrameAt(t, func(x, y int) string { return g.at(x, y).text }, fmt.Sprintf("tokens page %s %dx%d", icons, sz[0], sz[1]), r, true)
			for _, l := range m.rd.top().body {
				if l.Width() > r.W-2 {
					t.Errorf("%s %dx%d: a body line is %d cells in a %d-cell reader", icons, sz[0], sz[1], l.Width(), r.W-2)
					break
				}
			}
			checkReaderIconPairs(t, fmt.Sprintf("tokens page %s %dx%d", icons, sz[0], sz[1]), g, r)
		}
	}
}

// checkReaderIconPairs: every icon in the reader is followed by its blank
// cell, the pair inside the reader's frame, never against its border.
func checkReaderIconPairs(t *testing.T, name string, g *grid, r Rect) {
	t.Helper()
	icon := map[string]bool{}
	for _, c := range IconGlyphs {
		icon[c] = true
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if !icon[g.at(x, y).text] {
				continue
			}
			if x <= r.X || x+1 >= r.X+r.W-1 || g.at(x+1, y).text != " " {
				t.Errorf("%s: the icon pair at %d,%d is split or against the border", name, x, y)
			}
		}
	}
}

// tokFake is a poll reader that also reads tokens, counting the scans.
type tokFake struct {
	*fakeReader
	scans    int
	tokFails bool // only the tokens read fails; the rest of the poll goes on
}

func (f *tokFake) ProjectNames(context.Context) ([]string, error) {
	f.scans++
	if f.tokFails {
		return nil, fmt.Errorf("broker away")
	}
	return []string{"p"}, nil
}
func (f *tokFake) TokensOf(context.Context, string) (map[string]bus.TokenRow, bus.TokensMeta, bool, error) {
	return nil, bus.TokensMeta{}, false, nil
}
func (f *tokFake) QuotaHist(context.Context, int64) ([]bus.QuotaSample, error) { return nil, nil }

// The poll reads tokens (a keyspace SCAN) only while the U page is open:
// once on each opening (even after an earlier successful read), then every
// requestPoll ticks after the last attempt, a failed one included.
func TestTokensReadOnlyWhileTheUPageIsOpen(t *testing.T) {
	f := &tokFake{fakeReader: &fakeReader{calls: map[string]int{}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	poll := func(from, to int) {
		for tick := from; tick <= to; tick++ {
			pollOnce(context.Background(), f, st, tick)
		}
	}
	poll(1, 11)
	if f.scans != 0 {
		t.Fatalf("%d scans with the page closed", f.scans)
	}
	st.tokOpen = true
	poll(12, 12) // opened: read at once
	if f.scans != 1 {
		t.Fatalf("%d scans on opening, want 1", f.scans)
	}
	poll(13, 21) // then every requestPoll ticks after the last attempt: 17, 22 -> one more by 21
	if f.scans != 2 {
		t.Errorf("%d scans over the next 9 open ticks, want 2", f.scans)
	}
	st.tokOpen = false
	poll(22, 23)
	st.tokOpen = true
	before := f.scans
	poll(24, 24) // reopened after a successful read, off the cadence: read at once
	if f.scans != before+1 {
		t.Errorf("reopening did not read at once (%d -> %d)", before, f.scans)
	}
	f.tokFails = true // a failing read is retried at the cadence, not every tick
	before = f.scans
	poll(25, 33)
	if got := f.scans - before; got != 1 {
		t.Errorf("%d attempts in 9 ticks of failing reads, want 1 (tick 29)", got)
	}
	m := fixtureShell(t, 120, 40)
	if m.tokensOpen() {
		t.Error("the page reads as open before U")
	}
	if m = keys(t, m, 'U'); !m.tokensOpen() {
		t.Error("U did not open the page")
	}
}

// The pulse needs both facts: declared working and its pane seen busy.
func TestPulseNeedsWorkingAndBusy(t *testing.T) {
	now := shellNow
	for _, tc := range []struct {
		state string
		busy  bool
		want  bool
	}{{"working", true, true}, {"working", false, false}, {"idle", true, false}} {
		a := &agentState{state: tc.state, obsBusy: tc.busy, obsAt: now.Add(-5 * time.Second), stateAt: now, judgedAt: now}
		row := agentRow(iconsOff, "coder", a, false, TierWide, 140, now)
		if got := row[0].Fade > 0; got != tc.want {
			t.Errorf("%s busy=%v: glyph fade %d, want pulsing %v", tc.state, tc.busy, row[0].Fade, tc.want)
		}
		if got := pulsing(a, now); got != tc.want {
			t.Errorf("%s busy=%v: pulsing %v", tc.state, tc.busy, got)
		}
	}
	stale := &agentState{state: "working", obsBusy: true, obsAt: now.Add(-observedFresh - time.Second)}
	if pulsing(stale, now) {
		t.Error("a stale observation pulses")
	}
}

// The skeleton shimmers only within its 2 minutes, with the dispatcher up,
// the U page open and no reading of the open project; the fade schedule runs
// only while something animates, in true colour only.
func TestTokensSkeletonAndFadeSchedule(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	m := fixtureShell(t, 120, 40)
	m.cfg.settings.Fade = true
	m.snap.agents = map[string]*agentState{"coder": {state: "idle", registered: true, stateAt: shellNow}} // nothing else animates
	m.snap.asks = nil
	m.snap.dispatch = "ready"
	m.snap.tok = tokState{}
	if m.animated() {
		t.Fatal("animated with nothing to animate")
	}
	m = keys(t, m, 'U')
	if !m.tokensSkeleton() || !m.animated() {
		t.Fatal("no skeleton on a pending first reading")
	}
	if body := linesText(m.rd.top().body); !strings.Contains(body, "first reading pending") {
		t.Errorf("page without the skeleton:\n%s", body)
	}
	if cmd := m.scheduleFade(); cmd == nil {
		t.Error("the fade did not schedule for the skeleton")
	}
	m.sig.fading = false
	later := m
	later.now = m.sig.started.Add(skeletonFor)
	if later.tokensSkeleton() {
		t.Error("the skeleton outlived its 2 minutes")
	}
	// Rendered after the window: the open project reads ? with its reason, no shimmer.
	page := linesText(tokensPage(iconsOff, later.snap.tok, nil, nil, later.snap.agents, "agent-bus-monitor", later.now, 120, later.tokensSkeleton(), barView{}, false))
	if strings.Contains(page, "first reading pending") || !strings.Contains(page, "agent-bus-monitor") || !strings.Contains(page, "?") || !strings.Contains(page, "not read yet") {
		t.Errorf("after the window:\n%s", page)
	}
	later.snap.tok = tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor"}}}
	page = linesText(tokensPage(iconsOff, later.snap.tok, nil, nil, later.snap.agents, "agent-bus-monitor", later.now, 120, later.tokensSkeleton(), barView{}, false))
	if !strings.Contains(page, "no tokens recorded") || strings.Contains(page, "first reading pending") {
		t.Errorf("after the window, no key:\n%s", page)
	}
	down := m
	down.snap.dispatch = "down"
	if down.tokensSkeleton() {
		t.Error("a skeleton with the dispatcher down")
	}
	known := m
	known.snap.tok = tokState{polled: true, projects: []tokProject{{name: "agent-bus-monitor", known: true}}}
	if known.tokensSkeleton() {
		t.Error("a skeleton over a known reading")
	}
	withPalette(t, "dark", false, 16)
	if m.animated() {
		t.Error("animated in 16 colours")
	}
}

func linesText(ls []Line) string {
	var out []string
	for _, l := range ls {
		out = append(out, l.text())
	}
	return strings.Join(out, "\n")
}

// The U page writes each provider in its own colour (busmon-llm-colors): the
// account windows' label and the agents' provider column; an unknown
// provider keeps today's colour there (plain text in a window row).
func TestTokensPageProvidersInTheirColours(t *testing.T) {
	agents := map[string]*agentState{
		"coder":          {registered: true, pid: 1, identSession: "0198e5c4-coder-1", provider: "anthropic"},
		"reviewer-codex": {registered: true, pid: 2, provider: "openai", client: "codex"},
	}
	w := []bus.BudgetWindow{{Name: "5h", UsedPct: 40, ObservedAt: 1}}
	budgets := map[string]bus.BudgetSnapshot{"anthropic": {Windows: w}, "openai": {Windows: w}, "acme": {Windows: w}}
	roles := map[string]map[Role]bool{}
	for _, l := range tokensPage(iconsOff, tokFixtureState(shellNow), budgets, nil, agents, "agent-bus-monitor", shellNow, 120, false, barView{}, true) {
		for _, s := range l {
			// A segment of the text role merges with its neighbours: key it by
			// the provider name it starts with.
			p, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(s.Text)), " ") // a card's head is upper case (usage-cards)
			if roles[p] == nil {
				roles[p] = map[Role]bool{}
			}
			roles[p][s.Role] = true
		}
	}
	if !roles["anthropic"][RoleProvAnthropic] || roles["anthropic"][RoleMuted] || roles["anthropic"][RoleText] {
		t.Errorf("anthropic drawn in %v, want its own colour everywhere", roles["anthropic"])
	}
	if !roles["claude"][RoleProvAnthropic] || !roles["codex"][RoleProvOpenAI] { // the cards' heads, by the header's names
		t.Errorf("card heads drawn in %v and %v", roles["claude"], roles["codex"])
	}
	if !roles["openai"][RoleProvOpenAI] {
		t.Errorf("openai drawn in %v", roles["openai"])
	}
	if !roles["acme"][RoleText] || len(roles["acme"]) != 1 {
		t.Errorf("an unknown provider drawn in %v, want today's plain text", roles["acme"])
	}
}

// The Accounts zone as cards (usage-cards, the person's choice from master's
// mock-up): per provider a head line, its name upper case and at the right its
// busiest window; one row per window with a full-width thin bar, the figure
// and the reset; a pace sentence under the card; a rule between cards; no
// 24-hour spark.
func TestTokensPageAccountCards(t *testing.T) {
	pinLocal(t)
	now := shellNow
	at := func(d time.Duration) int64 { return now.Add(d).Unix() }
	budgets := map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{
			{Name: "5h", UsedPct: 13, ResetsAt: at(2 * time.Hour), ObservedAt: now.UnixMilli()},   // 60 % gone, 13 % used: room
			{Name: "7d", UsedPct: 86, ResetsAt: at(12 * time.Hour), ObservedAt: now.UnixMilli()}}, // the busiest
		},
		"openai": {Windows: []bus.BudgetWindow{
			{Name: "5h", UsedPct: 50, ResetsAt: at(4 * time.Hour), ObservedAt: now.UnixMilli()}}}, // 60 min gone, 50 % used: the limit in 60 more
	}
	for _, width := range []int{80, 120} {
		lines := tokensPage(iconsOff, tokFixtureState(now), budgets, nil, nil, "agent-bus-monitor", now, width, false, barView{}, true)
		page := linesText(lines)
		for _, l := range lines {
			if l.Width() > width {
				t.Fatalf("%d: a line is %d cells: %q", width, l.Width(), l.text())
			}
		}
		for _, s := range []string{"CLAUDE", "7d " + GlyphNone + " 86%", "CODEX", // the header's names (foureyes)
			GlyphNone + " resets " + now.Add(2*time.Hour).Local().Format("15:04"),
			"On track: you should reach the reset with room to spare.",
			"At this pace the limit comes around " + now.Add(60*time.Minute).Local().Format("15:04") + ", before the " + now.Add(4*time.Hour).Local().Format("15:04") + " reset."} {
			if !strings.Contains(page, s) {
				t.Errorf("%d: the page lacks %q:\n%s", width, s, page)
			}
		}
		if strings.Contains(page, "24h") {
			t.Errorf("%d: the 24-hour spark is still drawn", width)
		}
		for _, l := range lines { // the busiest window sits at the card's right edge, two cells in
			if txt := l.text(); strings.Contains(txt, "CLAUDE") && Width(strings.TrimRight(txt, " ")) != width-2 {
				t.Errorf("%d: the head ends at %d, want %d: %q", width, Width(strings.TrimRight(txt, " ")), width-2, txt)
			}
		}
		if !strings.Contains(page, strings.Repeat(glyphHorizontal, width-4)) {
			t.Errorf("%d: no rule between the cards", width)
		}
		for _, l := range strings.Split(page, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "5h ") && strings.Count(l, GlyphThinBar)+strings.Count(l, GlyphThinMark)+strings.Count(l, glyphVertical) < width/3 {
				t.Errorf("%d: the bar is not full width: %q", width, l)
			}
		}
	}
}

// A window at its limit is never "on track": the card says the limit is
// reached and when it resets.
func TestPaceSentenceAtTheLimit(t *testing.T) {
	pinLocal(t)
	now := shellNow
	reset := now.Add(40 * time.Minute)
	ws := []bus.BudgetWindow{{Name: "5h", UsedPct: 100, ResetsAt: reset.Unix(), ObservedAt: now.UnixMilli()}}
	if got, want := paceSentence(bus.BudgetSnapshot{Windows: ws}, now), "Limit reached: it resets at "+reset.Local().Format("15:04")+"."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An aged reading never feeds a forecast (foureyes on step 2): the card says
// how old it is instead.
func TestPaceSentenceAgedReading(t *testing.T) {
	now := shellNow
	ws := []bus.BudgetWindow{{Name: "5h", UsedPct: 50, ResetsAt: now.Add(4 * time.Hour).Unix(), ObservedAt: now.Add(-2 * time.Hour).UnixMilli()}}
	if got, want := paceSentence(bus.BudgetSnapshot{Windows: ws}, now), "Reading 2h old: no forecast."; got != want {
		t.Errorf("got %q, want %q", got, want)
	} // A window of unknown length has no pace, but its age is still said (reviewer-codex).
	custom := []bus.BudgetWindow{{Name: "custom", UsedPct: 50, ResetsAt: now.Add(4 * time.Hour).Unix(), ObservedAt: now.Add(-2 * time.Hour).UnixMilli()}}
	if got, want := paceSentence(bus.BudgetSnapshot{Windows: custom}, now), "Reading 2h old: no forecast."; got != want {
		t.Errorf("unknown length: got %q, want %q", got, want)
	}
}
