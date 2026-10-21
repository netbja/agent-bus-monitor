package rolecfg

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func skillDir(t *testing.T, root, name, desc string) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListSkillsReadsFrontMatter(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	skillDir(t, a, "agent-bus", "Coordinate with peer agents over the bus")
	skillDir(t, a, "tdd", "Test-driven development: red, green")
	skillDir(t, b, "tdd", "a shadowed copy") // the first root wins, as for the launcher
	skillDir(t, b, "diagnosing-bugs", "Diagnosis loop for hard bugs and performance regressions")
	got := ListSkills([]string{a, b, filepath.Join(a, "missing")})
	var names []string
	for _, s := range got {
		names = append(names, s.Name+"="+s.Description)
	}
	want := []string{"agent-bus=Coordinate with peer agents over the bus", "diagnosing-bugs=Diagnosis loop for hard bugs and performance regressions", "tdd=Test-driven development: red, green"}
	if !slices.Equal(names, want) {
		t.Fatalf("skills %q", names)
	}
}

func TestDraftSkills(t *testing.T) {
	skills := []SkillInfo{
		{"agent-bus", "Coordinate with peer agents over the bus"},
		{"diagnosing-bugs", "Diagnosis loop for hard bugs and performance regressions"},
		{"tdd", "Test-driven development: red, green, refactor"},
		{"codebase-design", "Design deep modules, interfaces and seams for performance"},
		{"domain-modeling", "Build a project's domain model and glossary"},
		{"frontend", "Build visual web pages and layout"},
	}
	got := DraftSkills("Investigate performance regressions in the payment service and design its interfaces", skills)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"agent-bus", "codebase-design", "diagnosing-bugs"}) {
		t.Fatalf("draft skills %v (%+v)", names, got)
	}
	if got[1].Score != 3 || !slices.Equal(got[1].Words, []string{"design", "interfaces", "performance"}) {
		t.Fatalf("codebase-design scored %+v", got[1])
	}
	if only := DraftSkills("zzz qqq", skills); len(only) != 1 || only[0].Name != "agent-bus" {
		t.Fatalf("no match: %+v", only)
	}
	// Never more than three besides agent-bus.
	many := DraftSkills("build design development diagnosis domain visual", skills)
	if len(many) != 4 {
		t.Fatalf("%d skills: %+v", len(many), many)
	}
}

func TestDraftRole(t *testing.T) {
	v1, err := Load(writeConfig(t, "[roles.master]\nmodel = \"sonnet\"\npermission = \"acceptEdits\"\ntier = \"boot\"\npops = true\nskills = [\"agent-bus-master\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	skills := []ScoredSkill{{Name: "agent-bus"}, {Name: "diagnosing-bugs", Score: 1}}
	snip, prompt, err := DraftRole(v1, "payments", "Investigate the payment service.", skills)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[roles.payments]", `model = "sonnet"`, `permission = "manual"`, `tier = "pop"`, `approval = "person"`, `skills = ["agent-bus", "diagnosing-bugs"]`} {
		if !strings.Contains(snip, want) {
			t.Fatalf("schema 1 snippet lacks %q:\n%s", want, snip)
		}
	}
	for _, want := range []string{"# Role: payments", "Investigate the payment service.", "## Never", "## Hand-back", "agentbus subscribe payments"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	// The snippet appended still loads.
	base, _ := os.ReadFile(v1.manifestPath)
	if _, err := Load(writeConfig(t, string(base)+"\n"+snip)); err != nil {
		t.Fatalf("schema 1 manifest + snippet: %v", err)
	}

	v2, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	snip2, _, err := DraftRole(v2, "payments", "x", skills)
	if err != nil || !strings.Contains(snip2, `profile = "claude-opus-manual"`) || strings.Contains(snip2, "model =") {
		t.Fatalf("schema 2 snippet (%v):\n%s", err, snip2)
	}
	base2, _ := os.ReadFile(repoPath(t, "roles.toml"))
	if _, err := Load(writeConfig(t, string(base2)+"\n"+snip2)); err != nil {
		t.Fatalf("schema 2 manifest + snippet: %v", err)
	}

	for _, bad := range []string{"master", "Bad*", ""} {
		if _, _, err := DraftRole(v2, bad, "x", skills); err == nil {
			t.Errorf("drafted %q", bad)
		}
	}
}
