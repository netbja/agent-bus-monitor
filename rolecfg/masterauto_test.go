package rolecfg

import "testing"

// The person (master-auto-default): master starts in Claude Code's auto
// mode, and only master. Its ask rules stay (Claude Code's auto mode still
// prompts on an explicit ask rule; deny rules block in every mode).
func TestMasterStartsInAutoMode(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range m.roleOrder {
		p, err := m.Resolve(role)
		if err != nil {
			t.Fatal(err)
		}
		auto := p.Options["permission"] == "auto"
		if auto != (role == "master") {
			t.Errorf("%s: permission %q", role, p.Options["permission"])
		}
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ask) == 0 {
		t.Fatal("master lost its ask rules (the person's approval of deploy and infra pops)")
	}
	p, _ := m.Resolve("master")
	args, err := build(p, r, "p", "prompt", "/tmp/prompt", "", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range args {
		if args[i] == "--permission-mode" && i+1 < len(args) && args[i+1] == "auto" {
			found = true
		}
	}
	if !found {
		t.Fatalf("master's launch: %v", args)
	}
}
