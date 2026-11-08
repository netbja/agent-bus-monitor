package rolecfg

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

// role-coder2 (the person, 2026-11-08): a second implementer, popped for
// parallel work. It loads from this repository's manifest as a pop role
// with coder's profile, client, model, skills and launch rules (deliver
// denied), and its role file carries the rules that keep two coders apart.
func TestCoder2Role(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := m.Resolve("coder2")
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Resolve("coder")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.RolesByTier("pop"), "coder2") || slices.Contains(m.RolesByTier("boot"), "coder2") {
		t.Errorf("coder2 is not a pop role: pop %v", m.RolesByTier("pop"))
	}
	if c2.Profile != c.Profile || c2.Client != c.Client || c2.Provider != c.Provider || c2.Model != c.Model {
		t.Errorf("coder2 runs %s/%s/%s, coder %s/%s/%s", c2.Profile, c2.Client, c2.Model, c.Profile, c.Client, c.Model)
	}
	if !slices.Equal(c2.Skills, c.Skills) {
		t.Errorf("coder2 skills %v, coder %v", c2.Skills, c.Skills)
	}
	r2, err := m.LaunchRules("coder2")
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.LaunchRules("coder")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r2.Allow, r.Allow) || !slices.Equal(r2.Deny, r.Deny) || !slices.Equal(r2.Ask, r.Ask) {
		t.Errorf("coder2 rules %+v, coder %+v", r2, r)
	}
	if !slices.Contains(r2.Deny, "Bash(agentbus deliver *)") {
		t.Errorf("coder2 may deliver: deny %v", r2.Deny)
	}
	// The launch itself: coder's, but for the role's name and prompt.
	env := []string{"AGENT_LAUNCH_DRYRUN=1", "AGENTBUS_REPO=" + repoPath(t, "."), "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
	dry := func(role string) string {
		var out, errw bytes.Buffer
		if err := Launch(m, role, "demo", env, &out, &errw, nil); err != nil {
			t.Fatalf("%s: %v (%s)", role, err, errw.String())
		}
		return out.String()
	}
	if got, want := dry("coder2"), strings.NewReplacer("--name demo:coder ", "--name demo:coder2 ", "/roles/coder.md", "/roles/coder2.md").Replace(dry("coder")); got != want {
		t.Errorf("coder2's launch differs from coder's beyond the name:\n%s\nwant:\n%s", got, want)
	}
	md, err := os.ReadFile(repoPath(t, "roles/coder2.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"own git worktree per task", "origin/main", "Never push", "One task at a time",
		"Do not touch a package another\n  task has in flight", "agentbus board", "Two coders never take the same task"} {
		if !strings.Contains(string(md), w) {
			t.Errorf("roles/coder2.md lacks %q", w)
		}
	}
}

// The person's rule (2026-11-08, every project): a step that waits on an
// unmerged PR never leaves the implementer idle; master dispatches the next
// independent item, the implementer takes it and says which. Each place
// that tells an implementer or master how to hold says so.
func TestNeverIdleOnAnUnmergedPR(t *testing.T) {
	for _, f := range []string{"roles/coder.md", "roles/coder2.md", "roles/master.md",
		"skills/agent-bus/SKILL.md", "skills/agent-bus-master/SKILL.md"} {
		b, err := os.ReadFile(repoPath(t, f))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		for _, want := range []string{"unmerged PR never leaves", "independent", "never", "origin/main"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", f, want)
			}
		}
		if !strings.Contains(text, `means "wait"`) && !strings.Contains(text, `never means "wait`) && !strings.Contains(text, `not "wait`) && !strings.Contains(text, `never "wait`) {
			t.Errorf("%s does not say no-stacked-PRs is not a wait", f)
		}
	}
}
