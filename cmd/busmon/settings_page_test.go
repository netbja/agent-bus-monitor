package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// settingsEnv is a config directory with config.toml and busmon.toml, and an
// environment reading it: never the person's own files.
func settingsEnv(t *testing.T, cfgText, legacyText string, env map[string]string) func(string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"config.toml": cfgText, "busmon.toml": legacyText} {
		if text != "" {
			if err := os.WriteFile(filepath.Join(dir, "agentbus", name), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	all := map[string]string{"XDG_CONFIG_HOME": dir}
	for k, v := range env {
		all[k] = v
	}
	return func(k string) string { return all[k] }
}

func settingRowOf(t *testing.T, groups []settingGroup, key string) settingRow {
	t.Helper()
	for _, g := range groups {
		for _, r := range g.Rows {
			if r.Key == key {
				return r
			}
		}
	}
	t.Fatalf("no row %s", key)
	return settingRow{}
}

// The row (settings page spec): the symbol says where the value comes from,
// the source column says it in agentbus config's words, the value in the
// person's words; a flag, the env or busmon.toml are read-only here.
func TestSettingsRows(t *testing.T) {
	getenv := settingsEnv(t,
		"[display]\ntheme = \"dark\"\nlimit = -3\n[waiting]\nsnooze = \"nonsense\"\n[identity]\nas = \"charlie\"\n",
		"[waiting]\nsnooze = \"20m\"\nfade = false\n",
		map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	groups := settingsGroups(getenv, settingsFlags{}, []config.Group{config.GroupDisplay, config.GroupAttention, config.GroupIdentity})
	if len(groups) != 3 || groups[0].Group != config.GroupDisplay || groups[2].Group != config.GroupIdentity {
		t.Fatalf("groups %+v", groups)
	}
	for _, c := range []struct {
		key, sym, label, value, source string
		editable                       bool
	}{
		{"display.theme", GlyphWorking, "Theme", "dark", "config.toml", true},
		{"display.limit", GlyphWorking, "Activity lines at start", "all", "config.toml", true},
		{"waiting.snooze", GlyphBlocked, "Reminder snooze", "20m", "config.toml line 5 refused", true},
		{"waiting.fade", GlyphNone, "Fade and slide", "off", "busmon.toml (legacy)", true},
		{"waiting.bell", GlyphIdle, "Bell", "on", "default", true},
		{"updates.check", GlyphNone, "Update check", "off", "env AGENT_BUS_NO_UPDATE_CHECK", false},
		{"identity.as", GlyphWorking, "Your name", "charlie", "config.toml", true},
	} {
		r := settingRowOf(t, groups, c.key)
		if r.Sym != c.sym || r.Label != c.label || r.Value != c.value || r.Source != c.source || r.Editable != c.editable {
			t.Errorf("%s: %+v, want %s %q %q %q editable %v", c.key, r, c.sym, c.label, c.value, c.source, c.editable)
		}
	}
	// A flag wins and is read-only here.
	groups = settingsGroups(getenv, settingsFlags{Theme: "light"}, []config.Group{config.GroupDisplay})
	if r := settingRowOf(t, groups, "display.theme"); r.Sym != GlyphNone || r.Value != "light" || r.Source != "flag busmon --theme" || r.Editable {
		t.Fatalf("flag: %+v", r)
	}
	// Every row of the three groups has a label of its own, never the key.
	for _, g := range settingsGroups(getenv, settingsFlags{}, []config.Group{config.GroupDisplay, config.GroupAttention, config.GroupIdentity}) {
		for _, r := range g.Rows {
			if r.Label == "" || strings.Contains(r.Label, ".") {
				t.Errorf("%s: label %q", r.Key, r.Label)
			}
		}
	}
}

// settingsShell is a shell whose settings page reads and writes the config
// directory of getenv, its theme applied by a stub that records the name.
func settingsShell(t *testing.T, getenv func(string) string, themed *string) Model {
	t.Helper()
	return shellWith(t, 120, 40, shellConfig{getenv: getenv, retheme: func(name string) (*Theme, error) {
		*themed = name
		return th, nil
	}}, false)
}

// press sends a key and runs what it returns until the settings are read.
func press(t *testing.T, m Model, k any) Model {
	t.Helper()
	var msg tea.Msg
	switch k := k.(type) {
	case rune:
		msg = tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)})
	case tea.Key:
		msg = tea.KeyPressMsg(k)
	}
	next, cmd := m.Update(msg)
	for cmd != nil {
		out := cmd()
		sm, ok := out.(settingsMsg)
		if !ok {
			break
		}
		next, cmd = next.(Model).Update(sm)
	}
	return next.(Model)
}

func selectSetting(t *testing.T, m Model, key string) Model {
	t.Helper()
	p := m.rd.top()
	for i, r := range p.set.rows() {
		if r.Key == key {
			p.sel, p.dirty = i, true
			m.refresh()
			return m
		}
	}
	t.Fatalf("no row %s", key)
	return m
}

func configText(t *testing.T, getenv func(string) string) string {
	t.Helper()
	b, err := os.ReadFile(config.Path(getenv))
	if err != nil {
		return ""
	}
	return string(b)
}

// C opens the page; an on/off key flips and writes at once and applies at
// once; the theme is chosen with ←→ and applied; a number goes through the
// editor, its kind and its rule, nothing written on a refusal; d asks y,
// naming what applies next; a value from the env is read-only.
func TestSettingsPageChanges(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n[display]\ntheme = \"dark\"\n[waiting]\nbell = false\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	p := m.rd.top()
	if p == nil || p.kind != ovSettings || !p.set.loaded {
		t.Fatalf("C opened %+v", p)
	}
	if !strings.Contains(p.title, "2 set here") || !strings.Contains(tokLinesText(p.body), "Theme") {
		t.Fatalf("page %q:\n%s", p.title, tokLinesText(p.body))
	}

	m = press(t, selectSetting(t, m, "waiting.bell"), tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "bell = true") || !m.cfg.settings.Bell || !strings.Contains(m.ui.flash, "applied") {
		t.Fatalf("bell: %q %v %q", configText(t, getenv), m.cfg.settings.Bell, m.ui.flash)
	}

	m = selectSetting(t, m, "display.theme")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "theme = \"light\"") || themed != "light" || !strings.Contains(m.ui.flash, "theme applied") {
		t.Fatalf("theme: %q %q %q", configText(t, getenv), themed, m.ui.flash)
	}

	before := configText(t, getenv)
	m = selectSetting(t, m, "waiting.snooze")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	for range 5 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	m = press(t, m, '0')
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if s := m.rd.top().set; s.edit != "waiting.snooze" || !strings.Contains(s.err, "above 0") || configText(t, getenv) != before {
		t.Fatalf("refused snooze: edit %q err %q file %q", s.edit, s.err, configText(t, getenv))
	}
	m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	for _, r := range "20m" {
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "snooze = \"20m\"") || m.cfg.settings.Snooze != 20*time.Minute || m.rd.top().set.edit != "" {
		t.Fatalf("snooze: %q %v", configText(t, getenv), m.cfg.settings.Snooze)
	}

	m = selectSetting(t, m, "display.theme")
	m = press(t, m, 'd')
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "the default auto will apply (y)") {
		t.Fatalf("unset confirmation:\n%s", body)
	}
	m = press(t, m, 'y')
	if strings.Contains(configText(t, getenv), "theme") || !strings.HasPrefix(configText(t, getenv), "# mine\n") {
		t.Fatalf("unset: %q", configText(t, getenv))
	}

	before = configText(t, getenv)
	m = press(t, selectSetting(t, m, "updates.check"), tea.Key{Code: tea.KeyEnter})
	if configText(t, getenv) != before || !strings.Contains(m.ui.flash, "env AGENT_BUS_NO_UPDATE_CHECK") {
		t.Fatalf("env row: %q %q", configText(t, getenv), m.ui.flash)
	}
	if m = press(t, m, tea.Key{Code: tea.KeyEscape}); m.rd.top() != nil && m.rd.top().kind == ovSettings {
		t.Fatal("Esc left the page open")
	}
}

// foureyes on part 2: Esc in the line editor leaves the file byte for byte;
// d is offered only where config.toml holds the key (● or a ▲ refused line),
// never on a busmon.toml row; its confirmation names busmon.toml's value when
// that is what applies next; a second Enter on an on/off while the first
// write is in flight is ignored, so one Enter is one flip.
func TestSettingsPageReviewFixes(t *testing.T) {
	getenv := settingsEnv(t,
		"# mine\n[waiting]\nsnooze = \"5m\" # short\nbell = false\npopup_after = \"nonsense\"\n",
		"[waiting]\nsnooze = \"20m\"\nfade = false\n", nil)
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')

	before := configText(t, getenv)
	m = press(t, selectSetting(t, m, "waiting.snooze"), tea.Key{Code: tea.KeyEnter})
	for range 2 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	for _, r := range "30m" { // a value the rule takes: only Esc keeps it out
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})
	if s := m.rd.top(); s == nil || s.kind != ovSettings || s.set.edit != "" || configText(t, getenv) != before {
		t.Fatalf("Esc in the editor: page %v file %q", s, configText(t, getenv))
	}

	m = press(t, m, 'd')
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "busmon.toml's 20m will apply (y)") {
		t.Fatalf("unset names busmon.toml's value:\n%s", body)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})

	m = press(t, selectSetting(t, m, "waiting.fade"), 'd')
	if s := m.rd.top().set; s.confirm != "" || strings.Contains(tokLinesText(m.rd.top().body), "will apply (y)") {
		t.Fatalf("d offered on a busmon.toml row: %q", s.confirm)
	}
	m = press(t, selectSetting(t, m, "waiting.popup_after"), 'd')
	if s := m.rd.top().set; s.confirm != "waiting.popup_after" {
		t.Fatalf("d not offered on a refused config.toml line: %q", s.confirm)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})

	m = selectSetting(t, m, "waiting.bell")
	next, first := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	next, second := next.(Model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if first == nil || second != nil {
		t.Fatalf("enter while a write is in flight: first %v second %v", first != nil, second != nil)
	}
	next, _ = next.(Model).Update(first())
	m = next.(Model)
	if !strings.Contains(configText(t, getenv), "bell = true") {
		t.Fatalf("one flip: %q", configText(t, getenv))
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "bell = false") {
		t.Fatalf("keys after the write: %q", configText(t, getenv))
	}
}

// busmon-icons §6: Icons is a Display row chosen with ←→ like the theme,
// applied at once; its sample line draws each mode's glyphs in that mode, so
// the person sees what the terminal draws.
func TestSettingsPageIcons(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	body := tokLinesText(m.rd.top().body)
	if !strings.Contains(body, "Icons") {
		t.Fatalf("no Icons row:\n%s", body)
	}
	for _, g := range []string{nfAlphaA, nfAlphaO, nfAlphaK, nfAlphaX, nfAlphaG, fontClaude, fontCodex, fontKimi, fontGrok, fontGemini} {
		if !strings.Contains(body, g+" ") {
			t.Errorf("the sample lacks %q followed by its blank cell", g)
		}
	}
	m = selectSetting(t, m, "display.icons")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "icons = \"nerd\"") || m.cfg.settings.Icons != "nerd" || !strings.Contains(m.ui.flash, "applied") {
		t.Fatalf("icons: %q %q %q", configText(t, getenv), m.cfg.settings.Icons, m.ui.flash)
	}
	m = selectSetting(t, m, "display.icons")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyLeft})
	m = press(t, m, tea.Key{Code: tea.KeyLeft})
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "icons = \"agent-font\"") || m.cfg.settings.Icons != "agent-font" {
		t.Fatalf("icons back: %q %q", configText(t, getenv), m.cfg.settings.Icons)
	}
}
