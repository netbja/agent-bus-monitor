package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// idle view §2: the page takes the file rule (30s refused, nothing written)
// and a good value applies at once.
func TestIdleViewSettingOnPage(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	before := configText(t, getenv)
	m = press(t, selectSetting(t, m, "display.idle_view_after"), tea.Key{Code: tea.KeyEnter})
	for range 5 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	for _, r := range "30s" {
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if s := m.rd.top().set; !strings.Contains(s.err, "from 1m to 24h") || configText(t, getenv) != before || m.cfg.settings.IdleView != 0 {
		t.Fatalf("30s: err %q file %q setting %v", s.err, configText(t, getenv), m.cfg.settings.IdleView)
	}
	for range 3 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	for _, r := range "10m" {
		m = press(t, m, r)
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(configText(t, getenv), "idle_view_after = \"10m\"") || m.cfg.settings.IdleView != 10*time.Minute || !strings.Contains(m.ui.flash, "applied") {
		t.Fatalf("10m: file %q setting %v flash %q", configText(t, getenv), m.cfg.settings.IdleView, m.ui.flash)
	}
}
