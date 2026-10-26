package rolecfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillsManifest(t *testing.T, profiles, roles string) *Manifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n" + profiles + "\n" + roles
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func writeSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallSkillsCreatesLinksAndIsIdempotent(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"review\"]\n[roles.reviewer]\ntier = \"pop\"\nskills = [\"review\"]\n")
	root := t.TempDir()
	source := writeSkill(t, filepath.Join(root, "repo-skills"), "review")
	dest := filepath.Join(root, "skills")
	env := []string{"HOME=" + root, "REPO_SKILLS=" + filepath.Join(root, "repo-skills"), "SKILLS_DEST=" + dest}
	var output bytes.Buffer
	if err := InstallSkills(m, env, &output); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dest, "review")
	got, err := os.Readlink(link)
	if err != nil || got != source {
		t.Fatalf("link = %q, %v; want %q", got, err, source)
	}
	if want := "linked review -> " + source + "\n"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	output.Reset()
	if err := InstallSkills(m, env, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("idempotent run printed %q", output.String())
	}
}

func TestInstallSkillsRefusesRegularFileBeforeWriting(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.first]\ntier = \"boot\"\nskills = [\"first\"]\n[roles.second]\ntier = \"pop\"\nskills = [\"second\"]\n")
	root := t.TempDir()
	skillsRoot := filepath.Join(root, "repo-skills")
	writeSkill(t, skillsRoot, "first")
	writeSkill(t, skillsRoot, "second")
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dest, "second")
	if err := os.WriteFile(regular, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallSkills(m, []string{"REPO_SKILLS=" + skillsRoot, "SKILLS_DEST=" + dest}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite non-symlink "+regular) {
		t.Fatalf("InstallSkills error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "first")); !os.IsNotExist(err) {
		t.Fatalf("first link was written before preflight completed: %v", err)
	}
	if got, err := os.ReadFile(regular); err != nil || string(got) != "keep" {
		t.Fatalf("regular file changed: %q, %v", got, err)
	}
}

func TestInstallSkillsUsesProviderSpecificDefaults(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"test\"\noptions = { permission = \"manual\" }\n"+
			"[profiles.codex]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.claude]\nprofile = \"claude\"\ntier = \"boot\"\nskills = [\"review\"]\n"+
			"[roles.codex]\nprofile = \"codex\"\ntier = \"pop\"\nskills = [\"review\"]\n")
	root := t.TempDir()
	source := writeSkill(t, filepath.Join(root, "repo-skills"), "review")
	if err := InstallSkills(m, []string{"HOME=" + root, "REPO_SKILLS=" + filepath.Join(root, "repo-skills")}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".agents", "skills")} {
		got, err := os.Readlink(filepath.Join(dest, "review"))
		if err != nil || got != source {
			t.Fatalf("link under %s = %q, %v; want %q", dest, got, err, source)
		}
	}
}

func TestInstallSkillsUsesAgentbusRepoDefault(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"review\"]\n")
	root := t.TempDir()
	source := writeSkill(t, filepath.Join(root, "skills"), "review")
	dest := filepath.Join(root, ".agents", "skills", "review")
	if err := InstallSkills(m, []string{"HOME=" + root, "AGENTBUS_REPO=" + root}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(dest)
	if err != nil || got != source {
		t.Fatalf("link = %q, %v; want %q", got, err, source)
	}
}

func TestInstallSkillsReplacesWrongSymlink(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"review\"]\n")
	root := t.TempDir()
	source := writeSkill(t, filepath.Join(root, "repo-skills"), "review")
	wrong := writeSkill(t, filepath.Join(root, "elsewhere"), "review")
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dest, "review")
	if err := os.Symlink(wrong, link); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkills(m, []string{"REPO_SKILLS=" + filepath.Join(root, "repo-skills"), "SKILLS_DEST=" + dest}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != source {
		t.Fatalf("replaced link = %q, %v; want %q", got, err, source)
	}
}

// bootstrap links with missing skills allowed: the agent starts without them
// (the launcher warns again), so a failed external-skills download never stops
// a first run. The plain install stays strict.
func TestInstallSkillsMissingOKWarnsAndLinksTheRest(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"review\", \"wayfinder\"]\n")
	root := t.TempDir()
	source := writeSkill(t, filepath.Join(root, "repo-skills"), "review")
	dest := filepath.Join(root, "skills")
	env := []string{"HOME=" + root, "REPO_SKILLS=" + filepath.Join(root, "repo-skills"), "POCOCK_SKILLS_ROOT=" + filepath.Join(root, "none"), "SKILLS_DEST=" + dest}
	var out, errOut bytes.Buffer
	if err := InstallSkills(m, env, &out); err == nil {
		t.Fatal("the strict install accepted a missing skill")
	}
	if err := InstallSkillsMissingOK(m, env, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(dest, "review")); err != nil || got != source {
		t.Fatalf("review link = %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "wayfinder")); !os.IsNotExist(err) {
		t.Fatalf("a missing skill got a link: %v", err)
	}
	if !strings.Contains(errOut.String(), "warning: skill wayfinder not found (run ./setup.sh install); coder starts without it") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}
