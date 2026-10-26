package rolecfg

import (
	"strings"
	"testing"
)

// A Claude Code profile names its permission mode: without one (or with
// "default") claude falls back to defaultMode in ~/.claude/settings.json,
// which decides silently (decision 2026-10-26, answer 1793039000836-0).
func TestClaudeProfileMustNameItsPermission(t *testing.T) {
	head := "schema_version = 2\n[defaults]\nprofile = \"cc\"\n[profiles.cc]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\n"
	tail := "[roles.a]\ntier = \"boot\"\n"
	for name, body := range map[string]string{
		"absent":  "",
		"default": "[profiles.cc.options]\npermission = \"default\"\n",
	} {
		_, err := Load(writeConfig(t, head+body+tail))
		if err == nil {
			t.Fatalf("%s permission accepted", name)
		}
		for _, want := range []string{"profile cc", "acceptEdits, auto, bypassPermissions, manual, dontAsk, plan", "defaultMode"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", name, err, want)
			}
		}
	}
	for _, perm := range []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"} {
		if _, err := Load(writeConfig(t, head+"[profiles.cc.options]\npermission = \""+perm+"\"\n"+tail)); err != nil {
			t.Errorf("%s refused: %v", perm, err)
		}
	}
	// Another client keeps its own default.
	kimi := "schema_version = 2\n[defaults]\nprofile = \"k\"\n[profiles.k]\nclient = \"kimi-cli\"\nprovider = \"moonshot\"\nmodel = \"m\"\n" + tail
	if _, err := Load(writeConfig(t, kimi)); err != nil {
		t.Fatalf("kimi without permission: %v", err)
	}
	// A fallback profile is checked too, by name.
	fb := head + "[profiles.cc.options]\npermission = \"manual\"\n[profiles.other]\nclient = \"claude-code\"\nprovider = \"x\"\nmodel = \"m\"\n" +
		"[roles.a]\ntier = \"boot\"\nfallback_profiles = [\"other\"]\n"
	if _, err := Load(writeConfig(t, fb)); err == nil || !strings.Contains(err.Error(), "profile other") {
		t.Fatalf("fallback without permission: %v", err)
	}
}

// Schema 1 names the permission on the role: the same rule, by role.
func TestClaudeLegacyRoleMustNameItsPermission(t *testing.T) {
	for _, body := range []string{"", "permission = \"default\"\n"} {
		_, err := Load(writeConfig(t, "[roles.a]\ntier = \"boot\"\nmodel = \"opus\"\n"+body))
		if err == nil || !strings.Contains(err.Error(), "role a") {
			t.Fatalf("legacy %q: %v", body, err)
		}
	}
}
