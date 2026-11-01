package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
