package rolecfg

import (
	"os"
	"slices"
	"testing"
)

var specialists = []string{"support", "dba", "web-design", "backend", "os-expert", "cicd"}

// examples/roles.specialists.toml is added to a project's manifest: with this
// repository's roles.toml it loads, every specialist is a pop role, and master
// pops the ones touching a live system only through an ask.
func TestSpecialistsExample(t *testing.T) {
	base, err := os.ReadFile(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	add, err := os.ReadFile(repoPath(t, "examples/roles.specialists.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(writeConfig(t, string(base)+"\n"+string(add)))
	if err != nil {
		t.Fatal(err)
	}
	pops := m.RolesByTier("pop")
	for _, role := range specialists {
		p, err := m.Resolve(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if !slices.Contains(pops, role) || !slices.Contains(p.Skills, "agent-bus") {
			t.Errorf("%s: pop %v skills %v", role, slices.Contains(pops, role), p.Skills)
		}
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"support", "web-design", "backend"} {
		if !slices.Contains(r.Allow, "Bash(agent-spawn "+role+" *)") {
			t.Errorf("master cannot pop %s without an ask: allow %v", role, r.Allow)
		}
	}
	for _, role := range []string{"dba", "os-expert", "cicd"} {
		if !slices.Contains(r.Ask, "Bash(agent-spawn "+role+" *)") {
			t.Errorf("master pops %s without the person: ask %v", role, r.Ask)
		}
	}
}

// Every role, shipped or specialist, has its prompt in roles/<role>.md.
func TestEveryRoleHasAPrompt(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range append(slices.Clone(m.roleOrder), specialists...) {
		if st, err := os.Stat(repoPath(t, "roles/"+role+".md")); err != nil || st.Size() == 0 {
			t.Errorf("roles/%s.md: %v", role, err)
		}
	}
}
