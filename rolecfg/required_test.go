package rolecfg

import (
	"strings"
	"testing"
)

// Self-repair step 4 (spec 2026-11-09-self-repair-watcher-design.md, 4.2):
// required = true marks a role whose closed tab the watcher recreates
// (absent: false). Only a boolean, and never on a pop: a pop comes back
// through master only.
func TestRoleRequired(t *testing.T) {
	const v2 = `schema_version = 2
[profiles.p]
client = "codex"
provider = "openai"
model = "test"
[roles.master]
profile = "p"
tier = "boot"
required = true
[roles.coder]
profile = "p"
tier = "boot"
required = false
[roles.sentinel]
profile = "p"
tier = "boot"
[roles.architect]
profile = "p"
tier = "pop"
`
	m, err := Load(writeConfig(t, v2))
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]bool{"master": true, "coder": false, "sentinel": false, "architect": false, "ghost": false} {
		if got := m.RoleRequired(role); got != want {
			t.Errorf("%s: required %v, want %v", role, got, want)
		}
	}
	const v1 = `[roles.master]
model = "sonnet"
permission = "manual"
tier = "boot"
required = true
`
	if m, err := Load(writeConfig(t, v1)); err != nil || !m.RoleRequired("master") {
		t.Fatalf("schema 1: %v", err)
	}
	for name, c := range map[string]struct{ text, want string }{
		"a pop":         {strings.Replace(v2, "tier = \"pop\"\n", "tier = \"pop\"\nrequired = true\n", 1), "role architect: required on a pop role"},
		"a string":      {strings.Replace(v2, "required = true", "required = \"yes\"", 1), "role master: required must be true or false"},
		"schema 1 pop":  {strings.Replace(v1, "tier = \"boot\"", "tier = \"pop\"", 1), "role master: required on a pop role"},
		"schema 1 type": {strings.Replace(v1, "required = true", "required = 1", 1), "role master: required must be true or false"},
	} {
		if _, err := Load(writeConfig(t, c.text)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	// A pop with required = false says nothing to refuse.
	if _, err := Load(writeConfig(t, strings.Replace(v2, "tier = \"pop\"\n", "tier = \"pop\"\nrequired = false\n", 1))); err != nil {
		t.Errorf("required = false on a pop: %v", err)
	}
}

// The shipped manifests mark the project's standing roles required: every
// boot role, and no pop. roles.toml gained them once a release that reads
// the key was installed (foureyes: an older parser refuses it, and every
// session reads the main checkout's roles.toml); a new role key follows the
// same order.
func TestShippedManifestsMarkTheBootRolesRequired(t *testing.T) {
	for _, name := range []string{"roles.toml", "examples/roles.mixed.toml"} {
		m, err := Load(repoPath(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range m.RoleNames() {
			if want := m.RoleTier(role) == "boot"; m.RoleRequired(role) != want {
				t.Errorf("%s: %s (%s) required %v", name, role, m.RoleTier(role), m.RoleRequired(role))
			}
		}
	}
}
