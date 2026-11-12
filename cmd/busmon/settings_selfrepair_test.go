package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Self-repair step 3 (spec 2026-11-09-self-repair-watcher-design.md, 4.5):
// the mode, the budgets, start at login and development mode are rows of
// the C page's Services group (the person: they all belong there).
func TestSettingsPageSelfRepairRows(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	groups := settingsGroups(getenv, settingsFlags{}, settingsPageGroups)
	for key, label := range map[string]string{
		"dispatch.self_repair": "Self-repair", "dispatch.self_repair_every": "Self-repair at most once per",
		"dispatch.self_repair_per_day": "Self-repairs a day per agent", "services.start_at_login": "Start services at login",
		"dev": "Development mode",
	} {
		r := settingRowOf(t, groups, key)
		if r.Label != label || len([]rune(r.Label)) > settingsLabelW || settingHelp(r) == "" {
			t.Errorf("%s: label %q help %q", key, r.Label, settingHelp(r))
		}
	}
	if r := settingRowOf(t, groups, "dispatch.self_repair"); r.Value != "on" || strings.Join(rowChoices(r), ",") != "on,off" {
		t.Errorf("outside dev: %q, choices %v: shadow is not offered", r.Value, rowChoices(r))
	}
	dev := settingsEnv(t, "dev = true\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	if r := settingRowOf(t, settingsGroups(dev, settingsFlags{}, settingsPageGroups), "dispatch.self_repair"); strings.Join(rowChoices(r), ",") != "on,off,shadow" {
		t.Errorf("in dev: choices %v", rowChoices(r))
	}
}

// shadow written by hand outside development mode: the row is refused at its
// line, and the person can still set it on the page.
func TestSettingsPageShadowRefused(t *testing.T) {
	getenv := settingsEnv(t, "[dispatch]\nself_repair = \"shadow\"\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	r := settingRowOf(t, settingsGroups(getenv, settingsFlags{}, settingsPageGroups), "dispatch.self_repair")
	if r.From != fromRefused || r.Source != "config.toml line 2 refused" || !r.Editable || !r.InFile {
		t.Fatalf("row %+v", r)
	}
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = selectSetting(t, m, "dispatch.self_repair")
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	m = press(t, m, tea.Key{Code: tea.KeyRight})
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "self_repair = \"off\"") {
		t.Fatalf("right of on: %q", configText(t, getenv))
	}
}

// foureyes: self_repair = false (a value its rule refuses) is refused at its
// line, reads off, and the person can set it on the page.
func TestSettingsPageSelfRepairFalse(t *testing.T) {
	getenv := settingsEnv(t, "[dispatch]\nself_repair = false\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	r := settingRowOf(t, settingsGroups(getenv, settingsFlags{}, settingsPageGroups), "dispatch.self_repair")
	if r.From != fromRefused || r.Source != "config.toml line 2 refused" || !r.Editable || !r.InFile {
		t.Fatalf("row %+v", r)
	}
}

// busmon's header says dev while development mode is on (4.5).
func TestHeaderDevMode(t *testing.T) {
	d := fullHeader()
	if strings.Contains(joined(headerLines(d, 160, TierWide)), " dev ") {
		t.Fatal("dev shown while off")
	}
	d.dev = true
	for w, tier := range map[int]Tier{160: TierWide, 100: TierMedium, 60: TierNarrow} {
		if text := joined(headerLines(d, w, tier)); !strings.Contains(text, " dev ") && !strings.HasPrefix(text, "dev ") {
			t.Errorf("%d columns: no dev mark: %q", w, text)
		}
	}
	if s := loadBusmonSettings(settingsEnv(t, "", "", map[string]string{"AGENT_BUS_DEV": "1"}), settingsFlags{}); !s.Dev {
		t.Error("AGENT_BUS_DEV=1 not read")
	}
}

// The watcher is not wired yet (self-repair steps 6-10): each of its settings
// says it takes effect when the watcher ships, so the person does not expect a
// relaunch today (master, 2026-11-11).
func TestSelfRepairHelpsSayNotYetWired(t *testing.T) {
	for _, k := range []string{"dispatch.self_repair", "dispatch.self_repair_every", "dispatch.self_repair_per_day"} {
		if h := settingHelps[k]; !strings.Contains(h, "takes effect when the watcher ships") {
			t.Errorf("%s help = %q", k, h)
		}
	}
}
