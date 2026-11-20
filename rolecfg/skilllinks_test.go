package rolecfg

import (
	"bytes"
	"path/filepath"
	"testing"
)

// TestSkillLinksIsInstallSkillsPlan: SkillLinks names each link InstallSkills
// writes, with the source it points to (agentbus update's strict check and its
// snapshot read this plan): one per destination and name, the last role's
// source winning, as installed.
func TestSkillLinksIsInstallSkillsPlan(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n[profiles.cc]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"test\"\n[profiles.cc.options]\npermission = \"manual\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"review\", \"tdd\"]\n[roles.master]\ntier = \"boot\"\nprofile = \"cc\"\nskills = [\"review\"]\n")
	root := t.TempDir()
	review := writeSkill(t, filepath.Join(root, "repo-skills"), "review")
	tdd := writeSkill(t, filepath.Join(root, "pocock", "engineering"), "tdd")
	env := []string{"HOME=" + root, "REPO_SKILLS=" + filepath.Join(root, "repo-skills"), "POCOCK_SKILLS_ROOT=" + filepath.Join(root, "pocock")}
	got, err := SkillLinks(m, env)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		filepath.Join(root, ".agents", "skills", "review"): review,
		filepath.Join(root, ".agents", "skills", "tdd"):    tdd,
		filepath.Join(root, ".claude", "skills", "review"): review,
	}
	if len(got) != len(want) {
		t.Fatalf("links %+v, want %v", got, want)
	}
	for _, l := range got {
		if want[l.Link] != l.Source {
			t.Errorf("%s -> %s, want %s", l.Link, l.Source, want[l.Link])
		}
	}
	var out bytes.Buffer
	if err := InstallSkills(m, env, &out); err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		if r, err := filepath.EvalSymlinks(l.Link); err != nil || r != l.Source {
			t.Errorf("installed %s resolves to %q (%v), plan says %s", l.Link, r, err, l.Source)
		}
	}
}

// TestSkillLinksMissingSkill: a skill no root holds is an error, as for
// InstallSkills.
func TestSkillLinksMissingSkill(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"absent\"]\n")
	root := t.TempDir()
	if _, err := SkillLinks(m, []string{"HOME=" + root, "REPO_SKILLS=" + root, "POCOCK_SKILLS_ROOT=" + root}); err == nil {
		t.Fatal("a missing skill is no error")
	}
}

// TestSkillDirs: each role client's destination once, even for a skill no
// root holds yet (the snapshot records these directories before the update
// fetches the skills).
func TestSkillDirs(t *testing.T) {
	m := writeSkillsManifest(t,
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"test\"\n[profiles.cc]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"test\"\n[profiles.cc.options]\npermission = \"manual\"\n",
		"[roles.coder]\ntier = \"boot\"\nskills = [\"absent\"]\n[roles.foureyes]\ntier = \"boot\"\nskills = [\"absent\"]\n[roles.master]\ntier = \"boot\"\nprofile = \"cc\"\n")
	root := t.TempDir()
	got, err := SkillDirs(m, []string{"HOME=" + root})
	want := []string{filepath.Join(root, ".agents", "skills"), filepath.Join(root, ".claude", "skills")}
	if err != nil || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%v %v, want %v", got, err, want)
	}
}
