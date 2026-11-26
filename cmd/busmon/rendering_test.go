package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// withRendering runs the test in mode and restores the one before.
func withRendering(t testing.TB, mode finedraw.Mode) {
	t.Helper()
	old := renderMode
	renderMode = mode
	t.Cleanup(func() { renderMode = old })
}

// tui-rendering §3 (the person's Q1, Q4): display.rendering resolved
// through the registry: an explicitly set display.rendering wins, then an
// explicitly set display.graphs, mapped, else auto (fine); a locale without
// UTF-8 forces ascii. The settings carry what decided it.
func TestRenderingSettings(t *testing.T) {
	utf8 := map[string]string{"LANG": "en_US.UTF-8"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{"LANG": "en_US.UTF-8"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, c := range []struct {
		name string
		toml string
		env  map[string]string
		want finedraw.Mode
		by   string
	}{
		{"neither", "", utf8, finedraw.ModeFine, finedraw.ByAuto},
		{"graphs braille in the file", "[display]\ngraphs = \"braille\"\n", utf8, finedraw.ModeFine, finedraw.ByGraphs},
		{"graphs block in the env", "", with("AGENT_BUS_BUSMON_GRAPHS", "block"), finedraw.ModeBasic, finedraw.ByGraphs},
		{"graphs ascii", "[display]\ngraphs = \"ascii\"\n", utf8, finedraw.ModeASCII, finedraw.ByGraphs},
		{"rendering basic over graphs braille", "[display]\nrendering = \"basic\"\ngraphs = \"braille\"\n", utf8, finedraw.ModeBasic, finedraw.ByRendering},
		{"rendering fine over graphs block env", "[display]\nrendering = \"fine\"\n", with("AGENT_BUS_BUSMON_GRAPHS", "block"), finedraw.ModeFine, finedraw.ByRendering},
		{"no UTF-8", "[display]\nrendering = \"fine\"\n", map[string]string{"LANG": "C"}, finedraw.ModeASCII, finedraw.ByLocale},
	} {
		s := loadBusmonSettings(settingsDir(t, c.toml, "", c.env), settingsFlags{})
		if s.Rendering != c.want || s.RenderingBy != c.by {
			t.Errorf("%s: %v by %q, want %v by %q", c.name, s.Rendering, s.RenderingBy, c.want, c.by)
		}
	}
}

// The fallback table for bars and gauges (§3): fine draws eighths on a
// coloured track from 256 colours up; 16 colours, no colour and basic keep
// whole cells; ascii draws # and -. The thin gauge's track is the fill
// faded in true colour, the dim role else.
func TestBarsFollowTheCaps(t *testing.T) {
	text := func(l Line) string { return l.text() }
	hasTrack := func(l Line) bool {
		for _, s := range l {
			if s.Track {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		name      string
		mode      finedraw.Mode
		noColor   bool
		colors    int
		bar       string
		track     bool
		thin      string
		thinFaded bool
	}{
		{"fine true colour", finedraw.ModeFine, false, 1 << 24, "█▎  ", true, "━━━━━", true},
		{"fine 256", finedraw.ModeFine, false, 256, "█▎  ", true, "━━━━━", false},
		{"fine 16", finedraw.ModeFine, false, 16, "▇░░░", false, "━──│─", false},
		{"fine no colour", finedraw.ModeFine, true, 1 << 24, "▇░░░", false, "━──│─", false},
		{"basic true colour", finedraw.ModeBasic, false, 1 << 24, "▇░░░", false, "━──│─", false},
		{"ascii", finedraw.ModeASCII, false, 1 << 24, "#---", false, "=--|-", false},
	} {
		withPalette(t, "dark", c.noColor, c.colors)
		withRendering(t, c.mode)
		bar := gaugeSegs(30, 4, RoleGauge, false)
		if text(bar) != c.bar || hasTrack(bar) != c.track {
			t.Errorf("%s: bar %q (track %v), want %q (track %v)", c.name, text(bar), hasTrack(bar), c.bar, c.track)
		}
		thin := thinGauge(20, 50, 5, RoleGauge, false)
		faded := false
		for _, s := range thin {
			faded = faded || s.Fade > 0
		}
		want := c.thin
		if c.mode == finedraw.ModeFine && c.colors >= 256 && !c.noColor {
			want = "━━━│━"
		}
		if text(thin) != want || faded != c.thinFaded {
			t.Errorf("%s: thin %q (faded %v), want %q (faded %v)", c.name, text(thin), faded, want, c.thinFaded)
		}
	}
}

// A fine bar's track shows from 256 colours up: the track cell has a
// background there, as in true colour; no colour has none.
func TestTrackColourAt256(t *testing.T) {
	for _, c := range []struct {
		colors  int
		noColor bool
		want    bool
	}{{1 << 24, false, true}, {256, false, true}, {1 << 24, true, false}} {
		withPalette(t, "dark", c.noColor, c.colors)
		on := th.termStyle(cellStyle{Role: RoleGauge, Track: true})
		off := th.termStyle(cellStyle{Role: RoleGauge})
		if got := on != off; got != c.want {
			t.Errorf("colors %d noColor %v: track drawn %v, want %v", c.colors, c.noColor, got, c.want)
		}
	}
}

// The Tokens page's sparklines follow the mode: braille in fine, blocks in
// basic, ascii in ascii.
func TestSparklinesFollowTheMode(t *testing.T) {
	for _, c := range []struct {
		mode finedraw.Mode
		has  string
	}{{finedraw.ModeFine, "⣀⡀⢀⣿⡇⡆⡄⢸⢰⢠"}, {finedraw.ModeBasic, "▁▂▃▄▅▆▇"}, {finedraw.ModeASCII, "_.-:=+*#"}} {
		withRendering(t, c.mode)
		page := linesText(tokensPage(iconsOff, drawCaps().SparkStyle(), tokFixtureState(shellNow), nil, nil, nil, "agent-bus-monitor", shellNow, 120, false, barView{}, true))
		if !strings.ContainsAny(page, c.has) {
			t.Errorf("%v: none of %q on the Tokens page:\n%s", c.mode, c.has, page)
		}
	}
}

// The settings page: Rendering is a Display row chosen with ←→, applied
// from the next frame without a restart; the detail says what draws and
// what decided it.
func TestSettingsPageRendering(t *testing.T) {
	withRendering(t, finedraw.ModeFine)
	getenv := settingsEnv(t, "[display]\ngraphs = \"braille\"\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1", "LANG": "en_US.UTF-8"})
	var themed string
	m := settingsShell(t, getenv, &themed)
	m.cfg.settings = loadBusmonSettings(getenv, settingsFlags{}) // as busmon reads them at start
	m = press(t, m, 'C')
	m = selectSetting(t, m, "display.rendering")
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "drawn fine, decided by display.graphs (mapped)") {
		t.Fatalf("no decided-by line:\n%s", body)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "rendering = \"basic\"") || renderMode != finedraw.ModeBasic || m.ui.flash != "rendering applied and saved" {
		t.Fatalf("rendering: %q mode %v flash %q", configText(t, getenv), renderMode, m.ui.flash)
	}
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "drawn basic, decided by display.rendering") {
		t.Fatalf("decided-by after the change:\n%s", body)
	}
}

// In ascii (a locale without UTF-8) the plan's bar and a task's progress
// bar draw in ASCII too: nothing a non-UTF-8 terminal cannot show.
func TestPlanAndProgressBarsInASCII(t *testing.T) {
	withRendering(t, finedraw.ModeASCII)
	if got := planBar(30, 4, RoleOK).text(); got != "=---" {
		t.Errorf("plan bar %q", got)
	}
	r := request{Task: "t", Tracked: true, State: "accepted", Accepted: shellNow.Add(-30 * time.Minute)}
	l, _ := progressLine(r, pace{usual: time.Hour, n: 6}, shellNow)
	if got := l.text(); !strings.Contains(got, "#####-----") || strings.Contains(got, finedraw.Full) { // finedraw.Bar (tui-rendering §4.4)
		t.Errorf("progress %q", got)
	}
}
