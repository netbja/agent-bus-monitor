package main

import (
	"os"
	"path/filepath"
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

// master's directive: a provider config.toml sets a threshold for, off the
// five, has its rows on the page too; one without a usage reader (none in
// quota.toml or built in) says so in its label, its threshold kept, inert.
func TestSettingsPageShowsAnyQuotaProvider(t *testing.T) {
	getenv := settingsEnv(t, "[quota.deepseek]\nhold = 80\n\n[quota.mistral]\nwarn = 60\n", "", nil)
	quotaToml := filepath.Join(getenv("XDG_CONFIG_HOME"), "agentbus", "quota.toml")
	if err := os.WriteFile(quotaToml, []byte("[providers.mistral]\nkind = \"unlimited\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var themed string
	m := press(t, settingsShell(t, getenv, &themed), 'C')
	groups := m.rd.top().set.groups
	for _, k := range []string{"quota.deepseek.warn", "quota.deepseek.drain", "quota.deepseek.hold", "quota.mistral.warn"} {
		if r := settingRowOf(t, groups, k); !r.Editable {
			t.Fatalf("%s: %+v", k, r)
		}
	}
	if r := settingRowOf(t, groups, "quota.deepseek.hold"); !strings.Contains(r.Label, "deepseek") || !strings.Contains(r.Label, "no usage reader") {
		t.Fatalf("deepseek label %q", r.Label)
	}
	if r := settingRowOf(t, groups, "quota.mistral.warn"); strings.Contains(r.Label, "no usage reader") {
		t.Fatalf("mistral has a reader in quota.toml: label %q", r.Label)
	}
	if h := settingHelp(settingRowOf(t, groups, "quota.deepseek.hold")); !strings.Contains(h, "[project.<name>.quota.deepseek]") {
		t.Fatalf("help %q", h)
	}
}
