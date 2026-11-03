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
func settingsEnv(t testing.TB, cfgText, legacyText string, env map[string]string) func(string) string {
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
		key          string
		from         settingFrom
		set          bool // config.toml sets it to other than its default (•)
		label, value string
		source       string
		editable     bool
	}{
		{"display.theme", fromFile, true, "Theme", "dark", "config.toml", true},
		{"display.limit", fromFile, true, "Activity lines at start", "all", "config.toml", true},
		{"waiting.snooze", fromRefused, false, "Reminder snooze", "20m", "config.toml line 5 refused", true},
		{"waiting.fade", fromElsewhere, false, "Fade and slide", "off", "busmon.toml (legacy)", true},
		{"waiting.bell", fromDefault, false, "Bell", "on", "default", true},
		{"updates.check", fromElsewhere, false, "Update check", "off", "env AGENT_BUS_NO_UPDATE_CHECK", false},
		{"identity.as", fromFile, true, "Your name", "charlie", "config.toml", true},
	} {
		r := settingRowOf(t, groups, c.key)
		if r.From != c.from || r.Set != c.set || r.Label != c.label || r.Value != c.value || r.Source != c.source || r.Editable != c.editable {
			t.Errorf("%s: %+v, want %v set %v %q %q %q editable %v", c.key, r, c.from, c.set, c.label, c.value, c.source, c.editable)
		}
	}
	// A key config.toml sets to its own default is in the file, never marked •.
	same := settingsGroups(settingsEnv(t, "[waiting]\nbell = true\n", "", nil), settingsFlags{}, []config.Group{config.GroupAttention})
	if r := settingRowOf(t, same, "waiting.bell"); r.From != fromFile || r.Set || !r.InFile {
		t.Fatalf("a default written in the file: %+v", r)
	}
	// A flag wins and is read-only here.
	groups = settingsGroups(getenv, settingsFlags{Theme: "light"}, []config.Group{config.GroupDisplay})
	if r := settingRowOf(t, groups, "display.theme"); r.From != fromElsewhere || r.Value != "light" || r.Source != "flag busmon --theme" || r.Editable {
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
	for g, grp := range p.set.groups {
		for i, r := range grp.Rows {
			if r.Key == key {
				p.set.group, p.set.keys, p.sel, p.dirty = g, true, i, true
				m.refresh()
				return m
			}
		}
	}
	t.Fatalf("no row %s", key)
	return m
}

// showGroup selects group g's first row, so the body shows its keys.
func showGroup(t *testing.T, m Model, g config.Group) Model {
	t.Helper()
	p := m.rd.top()
	for i, grp := range p.set.groups {
		if grp.Group == g {
			p.set.group, p.sel, p.dirty = i, 0, true
			m.refresh()
			return m
		}
	}
	t.Fatalf("no group %s", g)
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

// backup spec, "A person's setting only": the C page shows the three
// backup.after_* keys read-only, Enter says to edit config.toml yourself,
// d offers nothing, and the file is never touched.
func TestSettingsPageShowsAfterKeysReadOnly(t *testing.T) {
	getenv := settingsEnv(t, "[backup]\nafter_command = \"restic backup /b\"\nafter_env = [\"RESTIC_PASSWORD_FILE\"]\n", "",
		map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	groups := settingsGroups(getenv, settingsFlags{}, settingsPageGroups)
	for _, key := range []string{"backup.after_command", "backup.after_env", "backup.after_timeout"} {
		r := settingRowOf(t, groups, key)
		if r.Editable {
			t.Errorf("%s is editable on the page", key)
		}
	}
	if r := settingRowOf(t, groups, "backup.after_command"); r.Value != "restic backup /b" || r.Source != "config.toml" {
		t.Fatalf("row %+v", r)
	}
	for _, g := range groups {
		for _, r := range g.Rows {
			if g.Group == config.GroupBackup && !strings.HasPrefix(r.Key, "backup.after_") {
				t.Errorf("the page shows %s: only the after_* keys are asked for", r.Key)
			}
		}
	}
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	before := configText(t, getenv)
	m = press(t, selectSetting(t, m, "backup.after_command"), tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(m.ui.flash, "edit config.toml yourself") || configText(t, getenv) != before {
		t.Fatalf("Enter: flash %q", m.ui.flash)
	}
	m = press(t, m, 'd')
	if m.rd.top().set.confirm != "" || configText(t, getenv) != before {
		t.Fatal("d offered to unset a person's own setting")
	}
}

// The Notifications group (notifications spec, "busmon"): after Identity,
// every key editable; a list key is typed comma-separated and written as a
// TOML list; under the rows one muted line per target of notify.toml with
// its kind and last outcome, never its URL, topic or token.
func TestSettingsPageNotifications(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	dir := filepath.Dir(config.Path(getenv))
	os.WriteFile(filepath.Join(dir, "notify.toml"), []byte("[ntfy.phone]\ntopic = \"agentbus-secret-topic\"\ntoken = \"tk_never\"\n[discord.team]\nwebhook = \"https://discord.com/api/webhooks/1/hook-secret\"\n"), 0o600)
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	body := tokLinesText(showGroup(t, m, config.GroupNotifications).rd.top().body)
	var order []config.Group
	for _, g := range m.rd.top().set.groups {
		order = append(order, g.Group)
	}
	if indexOfGroup(order, config.GroupIdentity) < 0 || indexOfGroup(order, config.GroupNotifications) != indexOfGroup(order, config.GroupIdentity)+1 {
		t.Fatalf("Notifications not after Identity: %v", order)
	}
	for _, s := range []string{"Send notifications", "Targets", "Quiet hours", "phone", "ntfy", "team", "discord", "not listed", "edit it yourself"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
	for _, s := range []string{"agentbus-secret-topic", "tk_never", "hook-secret", "https://"} {
		if strings.Contains(body, s) {
			t.Fatalf("page shows %q:\n%s", s, body)
		}
	}
	m = press(t, selectSetting(t, m, "notify.on"), tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "on = true") {
		t.Fatalf("notify.on: %q", configText(t, getenv))
	}
	m = press(t, selectSetting(t, m, "notify.targets"), tea.Key{Code: tea.KeyEnter})
	for _, r := range "phone, team" {
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), `targets = ["phone", "team"]`) {
		t.Fatalf("targets: %q (err %q)", configText(t, getenv), m.rd.top().set.err)
	}
	if body := tokLinesText(showGroup(t, m, config.GroupNotifications).rd.top().body); !strings.Contains(body, "phone, team") || strings.Contains(body, "not listed") {
		t.Fatalf("after listing:\n%s", body)
	}
}

// reviewer-codex: a notify-last.json that cannot be read never reads as
// "never sent": the page says the outcome is unknown.
func TestSettingsNotifyTargetsUnreadableRecord(t *testing.T) {
	state := t.TempDir()
	getenv := settingsEnv(t, "[notify]\ntargets = [\"phone\"]\n", "", map[string]string{"XDG_STATE_HOME": state})
	os.WriteFile(filepath.Join(filepath.Dir(config.Path(getenv)), "notify.toml"), []byte("[ntfy.phone]\ntopic = \"agentbus-0123456789\"\n"), 0o600)
	os.MkdirAll(filepath.Join(state, "agentbus"), 0o700)
	os.WriteFile(filepath.Join(state, "agentbus", "notify-last.json"), []byte("{not json"), 0o600)
	lines := strings.Join(notifyTargetLines(getenv), "\n")
	if strings.Contains(lines, "never sent") || !strings.Contains(lines, "last send unknown") {
		t.Fatalf("%s", lines)
	}
}

func indexOfGroup(gs []config.Group, g config.Group) int {
	for i, x := range gs {
		if x == g {
			return i
		}
	}
	return -1
}

// settings-panels: at 92 cells and wider the groups are a sidebar beside the
// selected group's keys; narrower, a tab bar above them (tabBar's blocks, the
// selected one a pill), which gives way around the selected group with an
// ellipsis rather than cut it.
func TestSettingsPanelsLayout(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	set := &settingsPage{groups: settingsGroups(getenv, settingsFlags{}, settingsPageGroups), loaded: true}
	wide := settingsBody(set, 0, 96)
	for i, g := range set.groups {
		if !strings.Contains(wide[i].text(), string(g.Group)) || !strings.Contains(wide[i].text(), glyphVertical) {
			t.Fatalf("sidebar line %d lacks %s:\n%s", i, g.Group, tokLinesText(wide))
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(wide[0].text()), GlyphPrompt+" Display") {
		t.Fatalf("the selected group is not marked: %q", wide[0].text())
	}
	for _, w := range []int{91, 70, 40, 24} {
		set.group = 3 // Notifications
		lines := settingsBody(set, 0, w)
		tabs := lines[0]
		if strings.Contains(tokLinesText(lines), glyphVertical) {
			t.Fatalf("%d: a sidebar below 92 cells", w)
		}
		var pill *Seg
		for i := range tabs {
			if tabs[i].Pill {
				pill = &tabs[i]
			}
		}
		if pill == nil || strings.TrimSpace(pill.Text) != "Notifications" || tabs.Width() > w {
			t.Fatalf("%d: tabs %q (%d cells)", w, tabs.text(), tabs.Width())
		}
		if w < 60 && !strings.Contains(tabs.text(), GlyphEllipsis) {
			t.Fatalf("%d: no ellipsis for the hidden groups: %q", w, tabs.text())
		}
		for _, l := range lines {
			if l.Width() > w {
				t.Fatalf("%d: a line of %d cells: %q", w, l.Width(), l.text())
			}
		}
	}
}

// The values as controls: on/off a dot (on in the ok colour, off muted), a
// choice ‹ value ›, the rest plain; • only for a key config.toml sets to other
// than its default; a value from the env muted, a refused one in the attention
// colour.
func TestSettingsControls(t *testing.T) {
	getenv := settingsEnv(t, "[display]\ntheme = \"dark\"\n[waiting]\nbell = true\nfade = false\nsnooze = \"nonsense\"\n", "",
		map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	groups := settingsGroups(getenv, settingsFlags{}, settingsPageGroups)
	s := &settingsPage{}
	control := func(key string) Line { return settingControl(s, settingRowOf(t, groups, key)) }
	if c := control("waiting.bell"); c.text() != GlyphWorking+" on" || c[0].Role != RoleOK {
		t.Fatalf("on: %+v", c)
	}
	if c := control("waiting.fade"); c.text() != GlyphIdle+" off" || c[0].Role != RoleMuted {
		t.Fatalf("off: %+v", c)
	}
	if c := control("display.theme"); c.text() != GlyphChoiceL+" dark "+GlyphChoiceR || c[1].Role != RoleAccent {
		t.Fatalf("choice: %+v", c)
	}
	if c := control("updates.check"); c[0].Role != RoleMuted {
		t.Fatalf("env value not muted: %+v", c)
	}
	if c := control("waiting.snooze"); c[0].Role != RoleAttention {
		t.Fatalf("refused value not in the attention colour: %+v", c)
	}
	marked := func(key string) bool {
		return strings.Contains(settingsKeyRow(s, settingRowOf(t, groups, key), false, 26, 80).text(), GlyphSet)
	}
	if !marked("display.theme") || !marked("waiting.fade") || marked("waiting.bell") || marked("display.icons") {
		t.Fatalf("marks: theme %v fade %v bell %v icons %v", marked("display.theme"), marked("waiting.fade"), marked("waiting.bell"), marked("display.icons"))
	}
	// settings-focus: the values start in one column, the group's longest
	// label plus the gutter, never pushed to the right edge.
	var display settingGroup
	for _, g := range groups {
		if g.Group == config.GroupDisplay {
			display = g
		}
	}
	labelW := settingsLabelCol(display)
	if labelW != Width("Activity lines at start") {
		t.Fatalf("label column %d", labelW)
	}
	col := -1
	for _, r := range display.Rows {
		row := settingsKeyRow(s, r, false, labelW, 120).text()
		v := Width(row[:strings.Index(row, settingControl(s, r).text())]) // in cells
		if col < 0 {
			col = v
		}
		if v != col {
			t.Fatalf("%s: value at cell %d, want %d (%q)", r.Key, v, col, row)
		}
	}
	if want := 1 + 2 + 2 + labelW + settingsGutter; col != want {
		t.Fatalf("value column %d, want %d", col, want)
	}
	detail := tokLinesText(wrapSpans(settingsDetail(settingRowOf(t, groups, "display.theme")), 200, "", ""))
	for _, w := range []string{"Theme", "display.theme", "from config.toml", "(default: auto)", "read by busmon"} {
		if !strings.Contains(detail, w) {
			t.Errorf("detail lacks %q: %s", w, detail)
		}
	}
}

// settings-focus, the person's rule: the focus is on the groups or on the
// keys. Wide: ↑↓ choose the group (its keys follow), → or enter go to its
// keys, ← back, tab toggles; ↑↓ then move among the keys; an open choice
// takes ←→ for its value. The focused side is lit, the other muted. The
// footer advertises the page's own keys.
func TestSettingsGroupKeys(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	if m.mode() != ModeSettings || m.readerWidth() < settingsSideMin {
		t.Fatalf("mode %v width %d", m.mode(), m.readerWidth())
	}
	set := func() *settingsPage { return m.rd.top().set }
	down, up := tea.Key{Code: tea.KeyDown}, tea.Key{Code: tea.KeyUp}
	m = press(t, m, down)
	if set().group != 1 || set().keys {
		t.Fatalf("↓ on the groups: group %d keys %v", set().group, set().keys)
	}
	m = press(t, m, up)
	m = press(t, m, up)
	if set().group != 0 {
		t.Fatalf("↑ stops at the first group: %d", set().group)
	}
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	if !set().keys || m.rd.top().sel != 0 {
		t.Fatalf("→: keys %v sel %d", set().keys, m.rd.top().sel)
	}
	body := m.rd.top().body
	if c := body[0][0]; c.Role != RoleMuted || !strings.Contains(c.Text, GlyphPrompt) {
		t.Fatalf("the selected group is not muted with the focus on the keys: %+v", body[0])
	}
	if markRole := func() Role {
		for _, l := range m.rd.top().body {
			for _, sg := range l {
				if sg.Text == GlyphSelect {
					return sg.Role
				}
			}
		}
		return 0
	}(); markRole != RoleAccent {
		t.Fatal("the selected key is not lit with the focus on the keys")
	}
	m = press(t, m, down)
	if m.rd.top().sel != 1 || set().group != 0 {
		t.Fatalf("↓ on the keys: sel %d group %d", m.rd.top().sel, set().group)
	}
	m = press(t, m, tea.Key{Code: tea.KeyLeft})
	if set().keys {
		t.Fatal("← did not go back to the groups")
	}
	if c := m.rd.top().body[0][0]; c.Role != RoleAccent {
		t.Fatalf("the focused group is not lit: %+v", m.rd.top().body[0])
	}
	markRole := func() Role { // the selected key's ▶, wherever its line is
		for _, l := range m.rd.top().body {
			for _, sg := range l {
				if sg.Text == GlyphSelect {
					return sg.Role
				}
			}
		}
		t.Fatal("no selected key")
		return 0
	}
	if markRole() != RoleMuted {
		t.Fatal("the selected key is still lit with the focus on the groups")
	}
	m = press(t, m, tea.Key{Code: tea.KeyTab})
	if !set().keys {
		t.Fatal("tab did not move the focus to the keys")
	}
	m = press(t, m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	if set().keys {
		t.Fatal("shift+tab did not move it back")
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !set().keys || m.rd.top().set.edit != "" {
		t.Fatalf("enter on a group: keys %v edit %q", set().keys, set().edit)
	}
	m = selectSetting(t, m, "display.theme")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	if set().group != 0 || set().edit != "display.theme" || themeChoices[set().choice] != "dark" || !set().keys {
		t.Fatalf("on an open choice: group %d edit %q choice %d", set().group, set().edit, set().choice)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEscape})
	m = press(t, m, 'd')
	if set().confirm != "" {
		t.Fatal("d offered on a default key") // nothing in the file
	}
	foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 200, m.pageOffers, m.pageHint).text()
	for _, w := range []string{"move", "panel", "focus", "edit", "d reset", "D reset group", "back"} {
		if !strings.Contains(foot, w) {
			t.Errorf("footer lacks %q: %q", w, foot)
		}
	}
}

// Narrow (a tab bar above the keys): ←→ step the tabs, wrapping; ↓ or enter
// go to the keys; ↑ on the first key comes back to the tabs; ← from the keys
// too. The tab is a pill dimmed while the keys have the focus.
func TestSettingsGroupKeysNarrow(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	m := press(t, shellWith(t, 80, 24, shellConfig{getenv: getenv}, false), 'C')
	if m.readerWidth() >= settingsSideMin {
		t.Fatalf("reader %d: not narrow", m.readerWidth())
	}
	set := func() *settingsPage { return m.rd.top().set }
	m = press(t, m, tea.Key{Code: tea.KeyLeft})
	if set().group != len(set().groups)-1 || set().keys {
		t.Fatalf("← on the tabs wraps: group %d keys %v", set().group, set().keys)
	}
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	if set().group != 1 {
		t.Fatalf("→ on the tabs: group %d", set().group)
	}
	pill := func() Seg {
		for _, s := range m.rd.top().body[0] {
			if s.Pill {
				return s
			}
		}
		t.Fatal("no pill")
		return Seg{}
	}
	if pill().Fade != 0 {
		t.Fatal("the focused tab is dimmed")
	}
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	if !set().keys || m.rd.top().sel != 0 || pill().Fade != frameFade {
		t.Fatalf("↓ into the keys: keys %v sel %d fade %d", set().keys, m.rd.top().sel, pill().Fade)
	}
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	m = press(t, m, tea.Key{Code: tea.KeyUp})
	m = press(t, m, tea.Key{Code: tea.KeyUp})
	if set().keys {
		t.Fatal("↑ on the first key did not come back to the tabs")
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyLeft})
	if set().keys || set().group != 1 {
		t.Fatalf("← from the keys: keys %v group %d", set().keys, set().group)
	}
}

// D: every key of the group config.toml holds (a refused line too) is named
// and removed on y, in one write and one read back; the person's own
// backup.after_* stay and are said to; any other key keeps the file; a group
// with nothing set says so.
func TestSettingsResetGroup(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n[display]\ntheme = \"dark\"\nicons = \"nerd\"\n[waiting]\nsnooze = \"nonsense\"\n[backup]\nafter_command = \"restic backup /b\"\n", "",
		map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	before := configText(t, getenv)
	m = press(t, m, 'D')
	body := tokLinesText(m.rd.top().body)
	if !strings.Contains(body, "Reset Display? 2 keys set in config.toml (theme, icons) will be removed (y)") {
		t.Fatalf("D's confirmation:\n%s", body)
	}
	m = press(t, m, 'n')
	if configText(t, getenv) != before || m.rd.top().set.reset != nil {
		t.Fatal("a key other than y changed the file")
	}
	m = press(t, m, 'D')
	m = press(t, m, 'y')
	got := configText(t, getenv)
	if strings.Contains(got, "theme") || strings.Contains(got, "icons") || !strings.Contains(got, "snooze") ||
		!strings.HasPrefix(got, "# mine\n") || !strings.Contains(got, "after_command") {
		t.Fatalf("after D y: %q", got)
	}
	if themed != "auto" || !strings.Contains(m.ui.flash, "2 keys reset") {
		t.Fatalf("applied: theme %q flash %q", themed, m.ui.flash)
	}
	m = press(t, showGroup(t, m, config.GroupAttention), 'D')
	if !strings.Contains(tokLinesText(m.rd.top().body), "(snooze) will be removed") {
		t.Fatalf("a refused line is not offered:\n%s", tokLinesText(m.rd.top().body))
	}
	m = press(t, m, 'y')
	m = press(t, showGroup(t, m, config.GroupBackup), 'D')
	if m.rd.top().set.reset != nil || !strings.Contains(m.ui.flash, "after_command stay") {
		t.Fatalf("Backup's D: reset %v flash %q", m.rd.top().set.reset, m.ui.flash)
	}
	if !strings.Contains(configText(t, getenv), "after_command") {
		t.Fatal("D removed a person's own setting")
	}
	m = press(t, showGroup(t, m, config.GroupIdentity), 'D')
	if !strings.Contains(m.ui.flash, "nothing set in config.toml") {
		t.Fatalf("empty group: %q", m.ui.flash)
	}
}

// On a short page the selected key stays in view as it moves.
func TestSettingsSelectionInView(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	m := shellWith(t, 80, 24, shellConfig{getenv: getenv}, false)
	m = press(t, m, 'C')
	m = showGroup(t, m, config.GroupNotifications)
	for range 8 { // the first ↓ goes from the tabs to the keys
		m = press(t, m, tea.Key{Code: tea.KeyDown})
	}
	p := m.rd.top()
	if line := p.set.selLine; line < p.top || line >= p.top+m.readerRows() {
		t.Fatalf("selected line %d outside %d..%d", line, p.top, p.top+m.readerRows())
	}
}

// reviewer-codex: D removes every config.toml line of the group, also one a
// flag or the env shadows (the row keeps showing the value that applies),
// and names a person's own key the file holds as staying, shadowed or not.
func TestSettingsResetGroupShadowed(t *testing.T) {
	getenv := settingsEnv(t, "[display]\ntheme = \"dark\"\nicons = \"nerd\"\n[backup]\nafter_command = \"restic backup /b\"\n", "",
		map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1", "AGENT_BUS_BUSMON_ICONS": "off"})
	m := shellWith(t, 120, 40, shellConfig{getenv: getenv, flags: settingsFlags{Theme: "light"}}, false)
	m = press(t, m, 'C')
	if r := settingRowOf(t, m.rd.top().set.groups, "display.theme"); r.From != fromElsewhere || r.Editable {
		t.Fatalf("the flag's row: %+v", r)
	}
	m = press(t, m, 'D')
	body := tokLinesText(m.rd.top().body)
	if !strings.Contains(body, "2 keys set in config.toml (theme, icons) will be removed") {
		t.Fatalf("shadowed lines not named:\n%s", body)
	}
	// master: the confirmation says what still wins once the lines are gone.
	if flat := strings.Join(strings.Fields(strings.ReplaceAll(body, glyphVertical, " ")), " "); !strings.Contains(flat, "theme still follows flag busmon --theme") ||
		!strings.Contains(flat, "icons still follows env AGENT_BUS_BUSMON_ICONS") {
		t.Fatalf("the winners after the reset are not said:\n%s", body)
	}
	m = press(t, m, 'y')
	if got := configText(t, getenv); strings.Contains(got, "theme") || strings.Contains(got, "icons") {
		t.Fatalf("after D y: %q", got)
	}
	if _, kept := settingsResettable(groupOf(t, m, config.GroupBackup)); len(kept) != 1 || kept[0] != "backup.after_command" {
		t.Fatalf("kept %v", kept)
	}
}

func groupOf(t *testing.T, m Model, g config.Group) settingGroup {
	t.Helper()
	for _, grp := range m.rd.top().set.groups {
		if grp.Group == g {
			return grp
		}
	}
	t.Fatalf("no group %s", g)
	return settingGroup{}
}

// foureyes: below 92 cells the tab bar's two lines are counted, so the
// selected key stays in view there too.
func TestSettingsSelectionInViewNarrow(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	m := shellWith(t, 80, 24, shellConfig{getenv: getenv}, false)
	m = showGroup(t, press(t, m, 'C'), config.GroupNotifications)
	if m.readerWidth() >= settingsSideMin {
		t.Fatalf("reader %d cells: not the narrow layout", m.readerWidth())
	}
	rows := len(m.rd.top().set.rows())
	m = press(t, m, tea.Key{Code: tea.KeyDown}) // from the tabs to the first key
	for i := 1; i < rows; i++ {
		m = press(t, m, tea.Key{Code: tea.KeyDown})
		p := m.rd.top()
		want := m.rd.top().set.rows()[p.sel].Label
		top, n := p.top, m.readerRows()
		shown := tokLinesText(p.body[top:min(len(p.body), top+n)])
		if !strings.Contains(shown, GlyphSelect+"   "+want) && !strings.Contains(shown, GlyphSelect+" "+GlyphSet+" "+want) {
			t.Fatalf("row %d (%s) not on screen:\n%s", p.sel, want, shown)
		}
	}
}
