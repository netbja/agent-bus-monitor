package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// wip-limit r2 §3.3, Q3: board.wip_limit is the person's, set from Shift+C
// (or config.toml), which agentbus config set refuses. busmon's settings
// page writes it as the person (config.SetAsPerson, UnsetAsPerson); a
// PersonOnly key (backup.after_*) stays not editable there.
func TestSettingsPageWritesTheWIPLimit(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n", "", nil)
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = press(t, selectSetting(t, m, "board.wip_limit"), tea.Key{Code: tea.KeyEnter})
	if m.rd.top().set.edit != "board.wip_limit" {
		t.Fatalf("not editable: %+v", m.rd.top().set)
	}
	for range 3 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	m = press(t, m, '3')
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "wip_limit = 3") || m.rd.top().set.edit != "" {
		t.Fatalf("saved: %q, edit %q, err %q, flash %q", configText(t, getenv), m.rd.top().set.edit, m.rd.top().set.err, m.ui.flash)
	}
	m = press(t, selectSetting(t, m, "board.wip_limit"), 'd')
	m = press(t, m, 'y')
	if strings.Contains(configText(t, getenv), "wip_limit") {
		t.Fatalf("unset: %q", configText(t, getenv))
	}
	// Not enforced at this step (master): the help says so plainly.
	if h := settingHelp(settingRowOf(t, m.rd.top().set.groups, "board.wip_limit")); !strings.Contains(h, "Not enforced yet") || !strings.Contains(h, "next step") {
		t.Fatalf("help %q", h)
	}
	if r := settingRowOf(t, m.rd.top().set.groups, "backup.after_command"); r.Editable {
		t.Fatal("a PersonOnly key became editable")
	}
}
