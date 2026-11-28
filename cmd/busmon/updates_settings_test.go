package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// quiet-update-auto, the person's Q2: updates.window is the person's, set
// from Shift+C (written as the person) with a line saying what it does and
// that it is not in effect before the updater's unit (quiet-update step 4e); updates.auto and
// updates.max_wait are shown, not editable (config.toml alone).
func TestSettingsPageUpdatesRows(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n", "", nil)
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	m = press(t, selectSetting(t, m, "updates.window"), tea.Key{Code: tea.KeyEnter})
	if m.rd.top().set.edit != "updates.window" {
		t.Fatalf("not editable: %+v", m.rd.top().set)
	}
	for range 11 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	for _, r := range "01:30-02:15" {
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), `window = "01:30-02:15"`) {
		t.Fatalf("saved: %q, err %q", configText(t, getenv), m.rd.top().set.err)
	}
	groups := m.rd.top().set.groups
	for _, k := range []string{"updates.auto", "updates.max_wait"} {
		if r := settingRowOf(t, groups, k); r.Editable || r.Label == k {
			t.Errorf("%s: editable %v, label %q", k, r.Editable, r.Label)
		}
	}
	if h := settingHelp(settingRowOf(t, groups, "updates.window")); !strings.Contains(h, "03:00-05:00") || !strings.Contains(h, "Not in effect yet") {
		t.Fatalf("window help %q", h)
	}
}
