package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// busmon-icons §5: each icon is one Private Use rune, one cell by every width
// method busmon checks, and unique; the UI glyph set keeps its own count.
func TestIconsAreSingleCell(t *testing.T) {
	if len(IconGlyphs) == 0 {
		t.Fatal("no icon glyphs")
	}
	seen := map[string]bool{}
	for _, g := range IconGlyphs {
		r := []rune(g)
		if len(r) != 1 {
			t.Errorf("icon %q is %d runes", g, len(r))
			continue
		}
		pua := (r[0] >= 0xE000 && r[0] <= 0xF8FF) || (r[0] >= 0xF0000 && r[0] <= 0xFFFFD)
		if !pua || Width(g) != 1 || !widthsAgree(g) || seen[g] {
			t.Errorf("invalid icon U+%04X", r[0])
		}
		seen[g] = true
	}
}

// busmon-icons §3.1 row by row: agent-font keys on the client, then the
// provider; nerd on the provider only; off draws nothing; the person never.
func TestIconTables(t *testing.T) {
	cases := []struct{ client, provider, font, nerd string }{
		{"claude-code", "anthropic", "", "\U000f0b08"},
		{"codex", "openai", "", "\U000f0b16"},
		{"kimi-cli", "moonshot", "", "\U000f0b12"},
		{"grok", "xai", "", "\U000f0b1f"},
		{"agy", "google", "", "\U000f0b0e"},
		{"", "google", "", "\U000f0b0e"},
		{"", "deepseek", "", "\U000f0b0b"},
		{"", "xiaomi", "", "\U000f0b14"},
		{"", "local", "", "\U000f0b13"},
		{"codex", "local", "", "\U000f0b13"}, // the mismatched row: the tool's logo, the provider's box
		{"", "", "", ""},
		{"someone", "someone", "", ""},
	}
	for _, c := range cases {
		if got := agentIcon(iconsFont, c.client, c.provider); got != c.font {
			t.Errorf("agent-font %s/%s = %q, want %q", c.client, c.provider, got, c.font)
		}
		if got := agentIcon(iconsNerd, c.client, c.provider); got != c.nerd {
			t.Errorf("nerd %s/%s = %q, want %q", c.client, c.provider, got, c.nerd)
		}
		if got := agentIcon(iconsOff, c.client, c.provider); got != "" {
			t.Errorf("off %s/%s = %q", c.client, c.provider, got)
		}
	}
	// An account row is keyed by provider in both modes.
	if providerIcon(iconsFont, "anthropic") != "" || providerIcon(iconsNerd, "anthropic") != "\U000f0b08" || providerIcon(iconsOff, "anthropic") != "" {
		t.Error("providerIcon")
	}
	// Every client the launcher knows and every provider with a colour has an icon.
	for _, c := range rolecfg.Clients {
		if agentIcon(iconsFont, c.Client, "") == "" {
			t.Errorf("client %s has no agent-font icon", c.Client)
		}
	}
	for _, h := range providerHues {
		if providerIcon(iconsNerd, h.provider) == "" {
			t.Errorf("provider %s has no nerd icon", h.provider)
		}
	}
	for p := range quota.Builtins(func(string) (string, error) { return "/bin/x", nil }) {
		if providerIcon(iconsNerd, p) == "" {
			t.Errorf("built-in provider %s has no nerd icon", p)
		}
	}
	// Every glyph a table returns is in IconGlyphs.
	in := map[string]bool{}
	for _, g := range IconGlyphs {
		in[g] = true
	}
	for _, c := range cases {
		for _, g := range []string{c.font, c.nerd} {
			if g != "" && !in[g] {
				t.Errorf("icon %q not in IconGlyphs", g)
			}
		}
	}
}

// busmon-icons §7.1: an Agents row carries the icon pair between the state
// glyph and the name at 80 cells and wider, the message paying for it; a row
// with no icon gets two blank cells so names stay in one column; below 80
// there is no icon column; off draws today's row.
func TestAgentRowIconColumn(t *testing.T) {
	now := time.Now()
	agent := func(client, provider string) *agentState {
		return &agentState{state: "idle", stateAt: now, message: "a message long enough to be cut at any width we draw here", client: client, provider: provider}
	}
	for _, c := range []struct {
		tier  Tier
		width int
	}{{TierMedium, 80}, {TierMedium, 100}, {TierWide, 120}, {TierWide, 160}} {
		for _, mode := range []string{iconsNerd, iconsFont} {
			row := agentRow(mode, "coder", agent("claude-code", "anthropic"), false, c.tier, c.width, now)
			icon := agentIcon(mode, "claude-code", "anthropic")
			if row.Width() != c.width || !strings.Contains(row.text(), icon+" coder") {
				t.Fatalf("%v/%d %s: %q (width %d)", c.tier, c.width, mode, row.text(), row.Width())
			}
			for _, s := range row {
				if strings.HasPrefix(s.Text, icon) && s.Role != providerRole("anthropic", RoleText) {
					t.Errorf("the icon is %v, not the provider's colour", s.Role)
				}
			}
			off := agentRow(iconsOff, "coder", agent("claude-code", "anthropic"), false, c.tier, c.width, now).text()
			blank := agentRow(mode, "coder", agent("", "someone"), false, c.tier, c.width, now).text()
			if strings.Index(blank, "coder") != strings.Index(off, "coder")+2 || strings.Index(blank, "coder") != strings.Index(row.text(), "coder")-len(icon)+1 {
				t.Errorf("%v/%d %s: names not aligned:\n%q\n%q\n%q", c.tier, c.width, mode, off, row.text(), blank)
			}
			if h := agentHeader(mode, c.tier, c.width); c.tier == TierWide && (h.Width() != c.width || strings.Index(h.text(), "agent") != strings.Index(agentHeader(iconsOff, c.tier, c.width).text(), "agent")+2) {
				t.Errorf("%v/%d %s: header %q", c.tier, c.width, mode, h.text())
			}
		}
	}
	nar := agentRow(iconsNerd, "coder", agent("claude-code", "anthropic"), false, TierNarrow, 60, now).text()
	if nar != agentRow(iconsOff, "coder", agent("claude-code", "anthropic"), false, TierNarrow, 60, now).text() {
		t.Errorf("an icon below 80 cells: %q", nar)
	}
}

// iconTeam is the fixture's team with its clients and providers (a mixed
// team, spec §7.1): the off goldens keep the fixture as it is.
var iconTeam = map[string][2]string{
	"master": {"claude-code", "anthropic"}, "coder": {"claude-code", "anthropic"}, "sentinel": {"claude-code", "anthropic"},
	"foureyes": {"codex", "openai"}, "architect": {"codex", "openai"}, "ui-grok": {"grok", "xai"}, "qa": {"grok", "xai"},
	"ui-kimi": {"kimi-cli", "moonshot"}, "release": {"kimi-cli", "moonshot"}, "docs": {"", "google"},
}

// iconShell is the fixture shell at w x h with icons in mode.
func iconShell(t testing.TB, w, h int, mode string) Model {
	t.Helper()
	m := newShell(shellConfig{project: "agent-bus-monitor", self: "charlie"})
	m.cfg.settings.Icons = mode
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, tailMsg{events: shellEvents(shellNow)})
	snap := shellFixture(shellNow)
	for name, a := range snap.agents {
		a.judgedAt = shellNow
		if cp, ok := iconTeam[name]; ok {
			a.client, a.provider = cp[0], cp[1]
		}
	}
	return step(t, m, pollMsg{snap: snap, at: shellNow})
}

// busmon-icons §5: the dashboard in each mode, at the medium and wide tiers.
func TestIconGoldens(t *testing.T) {
	pinLocal(t)
	for _, mode := range []string{iconsNerd, iconsFont} {
		for _, size := range [][2]int{{80, 24}, {120, 30}} {
			t.Run(fmt.Sprintf("%s-%dx%d", mode, size[0], size[1]), func(t *testing.T) {
				withPalette(t, "dark", false, 1<<24)
				m := iconShell(t, size[0], size[1], mode)
				m.paint = newPainter(th)
				content := m.View().Content
				plain := m.grid().plain()
				for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
					if got := placed(content, size[0], size[1], method); got != plain {
						t.Fatalf("width method %v places the View otherwise:\n%s\nwant:\n%s", method, got, plain)
					}
				}
				base := fmt.Sprintf("dashboard-%dx%d-dark-%s", size[0], size[1], mode)
				checkGolden(t, base+".txt", plain+"\n")
				checkGolden(t, base+".ansi", content+"\n")
			})
		}
	}
}

// busmon-icons §5: off is today, byte for byte. The same model drawn in nerd,
// then switched to off as the C page does, draws the off goldens as they are.
func TestIconsOffIsToday(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{80, 24}, {140, 40}} {
		withPalette(t, "dark", false, 1<<24)
		m := iconShell(t, size[0], size[1], iconsNerd)
		m.paint = newPainter(th)
		if nerd := m.grid().plain(); !strings.Contains(nerd, nfAlphaA+" ") {
			t.Fatalf("%dx%d: nerd drew no icon", size[0], size[1])
		}
		m.cfg.settings.Icons = iconsOff
		m.refresh()
		base := fmt.Sprintf("dashboard-%dx%d-dark", size[0], size[1])
		want, err := os.ReadFile(filepath.Join("testdata", "golden", base+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := m.grid().plain() + "\n"; got != string(want) {
			t.Errorf("%s: off after nerd differs from today's golden:\n%s", base, got)
		}
		wantANSI, _ := os.ReadFile(filepath.Join("testdata", "golden", base+".ansi"))
		if got := m.View().Content + "\n"; got != string(wantANSI) {
			t.Errorf("%s: off after nerd: the ANSI differs from today's golden", base)
		}
	}
}

// busmon-icons §7.2: the header's gauges carry their provider's icon pair in
// the wide tier only, and only as far as no text is lost: the pairs are the
// first thing to go for room.
func TestHeaderGaugeIcons(t *testing.T) {
	now := time.Now()
	b := map[string]bus.BudgetSnapshot{
		"anthropic": {ObservedAt: now.UnixMilli(), Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 62, ResetsAt: now.Add(2 * time.Hour).Unix(), Minutes: 300, ObservedAt: now.UnixMilli()}}},
		"xai":       {ObservedAt: now.UnixMilli(), Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 40, ResetsAt: now.Add(2 * time.Hour).Unix(), Minutes: 300, ObservedAt: now.UnixMilli()}}},
	}
	d := headerData{project: "agent-bus-monitor", health: health{ok: true}, budgets: b, now: now, icons: iconsNerd}
	wide := headerText(d)
	if !strings.Contains(wide, nfAlphaA+" claude") || !strings.Contains(wide, nfAlphaX+" grok") {
		t.Fatalf("wide header without its icons:\n%s", wide)
	}
	for _, w := range []int{80, 100} {
		lines := headerLines(d, w, TierMedium)
		off := d
		off.icons = iconsOff
		want := headerLines(off, w, TierMedium)
		for i := range lines {
			if lines[i].text() != want[i].text() {
				t.Errorf("medium %d: icons in the header:\n%q\n%q", w, lines[i].text(), want[i].text())
			}
		}
	}
	// Short of room in the wide tier: the pairs go, the text stays.
	for w := 60; w <= 160; w++ {
		with, without := headerLines(d, w, TierWide), headerLines(headerData{project: d.project, health: d.health, budgets: b, now: now}, w, TierWide)
		for i := range with {
			strip := strings.NewReplacer(nfAlphaA+" ", "", nfAlphaX+" ", "").Replace(with[i].text())
			if !slices.Equal(strings.Fields(strip), strings.Fields(without[i].text())) { // the words; the clock's padding may shrink
				t.Fatalf("width %d: an icon cost text:\n%q\n%q", w, with[i].text(), without[i].text())
			}
		}
	}
}

// busmon-icons §5: the U page in each mode at 80 and 120 cells (Accounts rows
// by provider, the Agents zone by client then provider).
func TestIconTokensGoldens(t *testing.T) {
	pinLocal(t)
	for _, mode := range []string{iconsNerd, iconsFont} {
		for _, size := range [][2]int{{80, 44}, {120, 44}} {
			t.Run(fmt.Sprintf("%s-%dx%d", mode, size[0], size[1]), func(t *testing.T) {
				withPalette(t, "dark", false, 1<<24)
				m := iconShell(t, size[0], size[1], mode)
				m.snap.tok = tokFixtureState(shellNow)
				m = keys(t, m, 'U')
				if !m.rd.open() || m.rd.top().kind != ovTokens {
					t.Fatal("U opened no tokens page")
				}
				m.paint = newPainter(th)
				checkGolden(t, fmt.Sprintf("tokens-%dx%d-%s.txt", size[0], size[1], mode), m.grid().plain()+"\n")
			})
		}
	}
}

// busmon-icons §4: with icons, every U page line still fits its width (the
// account form chosen with the pair's two cells counted), and below 80 cells
// (the narrow tier) the page has no icon column.
func TestTokensPageIconsFit(t *testing.T) {
	pinLocal(t)
	at := shellNow.UnixMilli()
	win := func(n string, pct float64, reset int64) bus.BudgetWindow {
		return bus.BudgetWindow{Name: n, UsedPct: pct, ObservedAt: at, ResetsAt: shellNow.Unix() + reset}
	}
	full := map[string]bus.BudgetSnapshot{ // every window with a reset: each cell as wide as its form
		"anthropic": {Windows: []bus.BudgetWindow{win("5h", 62, 4*3600), win("7d", 55, 43*3600)}},
		"openai":    {Windows: []bus.BudgetWindow{win("5h", 100, 3600), win("7d", 23, 61*3600)}},
	}
	holds := map[string]int64{"openai": shellNow.Add(40 * time.Minute).Unix()}
	for _, budgets := range []map[string]bus.BudgetSnapshot{shellFixture(shellNow).budgets, full} {
		for _, mode := range []string{iconsNerd, iconsFont} {
			for width := 40; width <= 220; width++ {
				for _, l := range tokensPage(mode, tokFixtureState(shellNow), budgets, holds, nil, "agent-bus-monitor", shellNow, width, false, barView{}, true) {
					if l.Width() > width {
						t.Fatalf("%s at %d: a line is %d cells: %q", mode, width, l.Width(), l.text())
					}
				}
			}
		}
	}
	m := iconShell(t, 60, 40, iconsNerd)
	m.snap.tok = tokFixtureState(shellNow)
	if m = keys(t, m, 'U'); !m.rd.open() {
		t.Skip("no tokens page at 60 cells")
	}
	for _, l := range m.rd.top().body {
		for _, g := range IconGlyphs {
			if strings.Contains(l.text(), g) {
				t.Fatalf("an icon below 80 cells: %q", l.text())
			}
		}
	}
}
