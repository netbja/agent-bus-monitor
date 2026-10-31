package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// settingsDir is a config home holding the given config.toml and busmon.toml
// ("" = absent); its getenv knows XDG_CONFIG_HOME plus extra.
func settingsDir(t *testing.T, configTOML, busmonTOML string, extra map[string]string) func(string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config.toml": configTOML, "busmon.toml": busmonTOML} {
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "agentbus", name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	env := map[string]string{"XDG_CONFIG_HOME": dir}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }
}

const busmonFile = "[waiting]\npopup_after = \"2m\"\nsnooze = \"3m\"\n[recap]\nauto_after = \"4h\"\n[display]\ntheme = \"light\"\nlimit = 12\n[identity]\nas = \"fileperson\"\n"

func TestBusmonSettingsPrecedence(t *testing.T) {
	envs := map[string]string{"AGENT_BUS_BUSMON_LIMIT": "8", "AGENT_BUS_BUSMON_THEME": "auto", "AGENT_BUS_HUMAN": "env"}
	getenv := settingsDir(t, "", busmonFile, envs)
	got := loadBusmonSettings(getenv, settingsFlags{LimitSet: true, Limit: 7, Theme: "dark", As: "cli"})
	if got.Limit != 7 || got.Theme != "dark" || got.Identity != "cli" {
		t.Fatalf("explicit flags did not win: %+v", got)
	}
	got = loadBusmonSettings(getenv, settingsFlags{})
	if got.Limit != 8 || got.Theme != "auto" || got.Identity != "env" {
		t.Fatalf("environment did not win over file: %+v", got)
	}
	got = loadBusmonSettings(settingsDir(t, "", busmonFile, nil), settingsFlags{})
	if got.Limit != 12 || got.Theme != "light" || got.Identity != "fileperson" || got.PopupAfter != 2*time.Minute || got.Snooze != 3*time.Minute || got.AutoAfter != 4*time.Hour {
		t.Fatalf("file values not applied: %+v", got)
	}
	got = loadBusmonSettings(settingsDir(t, "", "", nil), settingsFlags{})
	if got.Limit != 25 || got.Theme != "auto" || got.PopupAfter != 5*time.Minute || got.Snooze != 15*time.Minute || got.AutoAfter != 8*time.Hour || !got.Bell || !got.Fade || !got.UpdateCheck || got.Note != "" {
		t.Fatalf("defaults changed: %+v", got)
	}
}

// config.toml wins over busmon.toml key by key; a key it lacks still comes
// from busmon.toml.
func TestConfigTOMLOverBusmonTOMLPerKey(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "[display]\nlimit = 40\n[updates]\ncheck = false\n", busmonFile, nil), settingsFlags{})
	if got.Limit != 40 || got.Theme != "light" || got.Snooze != 3*time.Minute || got.UpdateCheck {
		t.Fatalf("per-key mix: %+v", got)
	}
	// An ignored env value falls to the files: the spec's example.
	got = loadBusmonSettings(settingsDir(t, "[display]\nlimit = 40\n", "", map[string]string{"AGENT_BUS_BUSMON_LIMIT": "abc"}), settingsFlags{})
	if got.Limit != 40 {
		t.Fatalf("limit with an ignored env = %d, want 40", got.Limit)
	}
	got = loadBusmonSettings(settingsDir(t, "", "", map[string]string{"AGENT_BUS_BUSMON_LIMIT": "-1"}), settingsFlags{})
	if got.Limit != -1 {
		t.Fatalf("AGENT_BUS_BUSMON_LIMIT=-1 = %d, want -1 (all history)", got.Limit)
	}
}

func TestLoadBusmonSettingsInvalidFileDoesNotFail(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "", "[waiting\npopup_after = nope", nil), settingsFlags{Theme: "light"})
	if got.Theme != "light" || got.PopupAfter != 5*time.Minute || !strings.Contains(got.Note, "busmon.toml:2: does not parse; ignored") {
		t.Fatalf("invalid file blocked or corrupted settings: %+v", got)
	}
}

func TestBusmonSettingsInvalidValuesFallBack(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "", "[waiting]\npopup_after = \"lots\"\nbell = false\n[display]\ntheme = \"neon\"\n", nil), settingsFlags{})
	if got.PopupAfter != 5*time.Minute || got.Theme != "auto" || got.Bell || !strings.Contains(got.Note, "waiting.popup_after:") || !strings.Contains(got.Note, "display.theme:") {
		t.Fatalf("invalid values did not fall back with a note: %+v", got)
	}
}

// An invalid env theme or name still stops busmon, as it always has: the raw
// value reaches the validation that refuses it.
func TestInvalidEnvStillStopsBusmon(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "[display]\ntheme = \"dark\"\n", "", map[string]string{"AGENT_BUS_BUSMON_THEME": "neon", "AGENT_BUS_HUMAN": "Not Valid"}), settingsFlags{})
	if _, err := ResolveTheme(got.Theme, "", "", false, 1<<24); err == nil {
		t.Errorf("an invalid AGENT_BUS_BUSMON_THEME was accepted: %+v", got)
	}
	if err := got.validateIdentity(); err == nil {
		t.Errorf("an invalid AGENT_BUS_HUMAN was accepted: %+v", got)
	}
}

func TestUpdateCheckOptOut(t *testing.T) {
	for raw, want := range map[string]bool{"1": false, "0": true, "yes": true, "": true} {
		got := loadBusmonSettings(settingsDir(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": raw}), settingsFlags{})
		if got.UpdateCheck != want {
			t.Errorf("AGENT_BUS_NO_UPDATE_CHECK=%q: check %v, want %v", raw, got.UpdateCheck, want)
		}
	}
}
