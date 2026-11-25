package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// quota-per-provider: Shift+C shows the quota defaults and each provider's
// own warn, drain and hold, the person's to set (SetAsPerson), with a help
// line saying what 0 does and where a project's value goes; the save message
// says it applies live (the dispatcher reads it at its next pass).
func TestSettingsPageWritesAProviderQuota(t *testing.T) {
	getenv := settingsEnv(t, "# mine\n", "", nil)
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	groups := m.rd.top().set.groups
	for _, k := range []string{"dispatch.quota_warn", "dispatch.quota_drain", "dispatch.quota_hold", "quota.anthropic.warn", "quota.xai.hold"} {
		if r := settingRowOf(t, groups, k); !r.Editable || r.Label == k {
			t.Fatalf("%s: %+v", k, r)
		}
	}
	h := settingHelp(settingRowOf(t, groups, "quota.openai.hold"))
	for _, want := range []string{"openai", "0 uses", "[project.<name>.quota.openai]", "agents cannot change it"} {
		if !strings.Contains(h, want) {
			t.Fatalf("help %q lacks %q", h, want)
		}
	}
	m = press(t, selectSetting(t, m, "quota.openai.hold"), tea.Key{Code: tea.KeyEnter})
	if m.rd.top().set.edit != "quota.openai.hold" {
		t.Fatalf("not editable: %+v", m.rd.top().set)
	}
	for range 4 {
		m = press(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	m = press(t, m, '8')
	m = press(t, m, '0')
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if text := configText(t, getenv); !strings.Contains(text, "[quota.openai]") || !strings.Contains(text, "hold = 80") {
		t.Fatalf("saved: %q, err %q", text, m.rd.top().set.err)
	}
	if m.ui.flash != "saved: applies live" {
		t.Fatalf("flash %q", m.ui.flash)
	}
}
