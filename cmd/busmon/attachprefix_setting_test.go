package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
)

// P1-8: attach.prefix is the person's to change in Settings, through the
// rule agentbus attach applies to a prefix that comes from the setting (not
// --prefix): it must be verified free for every client the project's roles
// run on (their own profiles and fallbacks), read from the embedded client
// profiles. Anything else is refused there, naming each client and the keys
// free for it, and nothing is written (foureyes).
func TestSettingsAttachPrefixGate(t *testing.T) {
	manifest := func(t *testing.T, clients ...string) string {
		dir := t.TempDir()
		body := "schema_version = 2\n[defaults]\nprofile = \"p0\"\n"
		for i, c := range clients {
			body += "[profiles.p" + string(rune('0'+i)) + "]\nclient = \"" + c + "\"\nprovider = \"x" + string(rune('0'+i)) + "\"\nmodel = \"m\"\n"
			if c == "claude-code" {
				body += "options = { permission = \"manual\" }\n"
			}
		}
		body += "[roles.coder]\ntier = \"boot\"\n"
		if len(clients) > 1 {
			body += "fallback_profiles = [\"p1\"]\n"
		}
		p := filepath.Join(dir, "roles.toml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	edit := func(t *testing.T, roles, key string) (string, string) {
		getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1", "ROLES_TOML": roles})
		var themed string
		m := press(t, settingsShell(t, getenv, &themed), 'C')
		m = showGroup(t, m, config.GroupDisplay)
		m = press(t, selectSetting(t, m, "attach.prefix"), tea.Key{Code: tea.KeyEnter})
		for range 8 {
			m = press(t, m, tea.Key{Code: tea.KeyBackspace})
		}
		for _, r := range key {
			m = press(t, m, r)
		}
		m = press(t, m, tea.Key{Code: tea.KeyEnter})
		return configText(t, getenv), m.rd.top().set.err
	}
	claudeOnly := manifest(t, "claude-code")
	if file, e := edit(t, claudeOnly, "ctrl+]"); strings.Contains(file, "prefix") || !strings.Contains(e, "ctrl+] is not verified free for claude (free for it: f12)") {
		t.Fatalf("an unverified key (Claude Code binds ctrl+]): %q, err %q", file, e)
	}
	if file, e := edit(t, claudeOnly, "f12"); !strings.Contains(file, `prefix = "f12"`) {
		t.Fatalf("f12 on Claude Code: %q, err %q", file, e)
	}
	// A fallback profile on a client with no verified key: attach would
	// refuse a swapped agent, so the setting is refused.
	withAgy := manifest(t, "claude-code", "agy")
	if file, e := edit(t, withAgy, "f12"); strings.Contains(file, "prefix") || !strings.Contains(e, "agy (no key verified free)") {
		t.Fatalf("f12 with an agy fallback: %q, err %q", file, e)
	}
	// An alt profile is a client the role may run on too (pop --profile,
	// fable-profile-architect): one on agy refuses the setting as a fallback does.
	altAgy := manifest(t, "claude-code", "agy")
	if b, err := os.ReadFile(altAgy); err == nil {
		os.WriteFile(altAgy, []byte(strings.Replace(string(b), "fallback_profiles", "alt_profiles", 1)), 0o644)
	}
	if file, e := edit(t, altAgy, "f12"); strings.Contains(file, "prefix") || !strings.Contains(e, "agy (no key verified free)") {
		t.Fatalf("f12 with an agy alternate: %q, err %q", file, e)
	}
	// Codex records f12 free (its f12-at-input probe, 2026-11-16): a codex
	// fallback lets it through.
	if file, e := edit(t, manifest(t, "claude-code", "codex"), "f12"); !strings.Contains(file, `prefix = "f12"`) {
		t.Fatalf("f12 with a codex fallback: %q, err %q", file, e)
	}
	// Roles that cannot be read, or that rolecfg.Load refuses (a Claude Code
	// profile without its permission), never let a key through unjudged.
	if file, e := edit(t, filepath.Join(t.TempDir(), "none.toml"), "f12"); strings.Contains(file, "prefix") || !strings.Contains(e, "roles.toml") {
		t.Fatalf("no roles: %q, err %q", file, e)
	}
	broken := filepath.Join(t.TempDir(), "roles.toml")
	os.WriteFile(broken, []byte("schema_version = 2\n[defaults]\nprofile = \"p0\"\n[profiles.p0]\nclient = \"claude-code\"\nprovider = \"x\"\nmodel = \"m\"\n[roles.coder]\ntier = \"boot\"\n"), 0o644)
	if file, e := edit(t, broken, "f12"); strings.Contains(file, "prefix") || e == "" {
		t.Fatalf("a profile that does not resolve: %q, err %q", file, e)
	}
}
