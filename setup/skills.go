package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// CheckPath is setup.sh's PATH check: the bin dir on PATH, and nothing
// earlier on it shadowing the new binaries (a dry run skips the shadow check).
func (in *Installer) CheckPath(path string) {
	bin := in.Opt.BinDir
	if !strings.Contains(":"+path+":", ":"+bin+":") {
		in.O.Warn(bin + " is NOT in your current PATH!")
		in.O.Info("Add it to your shell configuration (e.g. ~/.bashrc or ~/.zshrc):")
		in.O.Info(`  export PATH="` + bin + `:$PATH"`)
		return
	}
	in.O.OK("PATH: " + bin + " is in your active PATH")
	if in.Opt.DryRun {
		return
	}
	for _, name := range []string{"agentbus", "busmon"} {
		if found, err := in.R.LookPath(name); err == nil && found != filepath.Join(bin, name) {
			in.O.Warn(name + " on PATH is " + found + ", which shadows " + bin + "/" + name + "; remove it or put " + bin + " first.")
		}
	}
}

func (in *Installer) skillsRepo() string {
	if r := in.Getenv("SKILLS_REPO"); r != "" {
		return r
	}
	return "https://github.com/mattpocock/skills.git"
}

// ExternalSkills is setup.sh's external-skills step: mattpocock/skills
// cloned at its pin into SkillsHome and marked as setup's; a folder setup
// did not make (no marker) is the person's and left as it is.
func (in *Installer) ExternalSkills(ctx context.Context) {
	if !in.Opt.InstallSkills {
		return
	}
	home, ref, repo := SkillsHome(in.Getenv), SkillsRef(in.Getenv), in.skillsRepo()
	mark := filepath.Join(home, ".git", "agentbus-setup")
	git := func(args ...string) ([]byte, error) {
		return in.R.Run(ctx, Cmd{Name: "git", Args: args})
	}
	_, homeErr := os.Stat(home)
	_, markErr := os.Stat(mark)
	gitDir, gitErr := os.Stat(filepath.Join(home, ".git"))
	switch {
	case in.Opt.DryRun:
		in.O.Info("[dry-run] would fetch " + repo + " " + ref + " into " + home)
	case homeErr == nil && markErr != nil:
		in.O.Warn(home + " was not created by setup: left as it is (agents use the skills it holds)")
	case gitErr == nil && gitDir.IsDir():
		log, err := git("-C", home, "fetch", "--quiet", "--depth", "1", "origin", "refs/tags/"+ref+":refs/tags/"+ref)
		if err != nil {
			more, err2 := git("-C", home, "fetch", "--quiet", "--depth", "1", "origin", ref)
			log, err = append(log, more...), err2
		}
		if err != nil {
			showTail(in.O.err, log)
			in.O.Warn("could not update the external skills in " + home + " (kept as they are).")
			return
		}
		if _, err := git("-C", home, "checkout", "--quiet", ref); err != nil {
			if _, err := git("-C", home, "checkout", "--quiet", "FETCH_HEAD"); err != nil {
				// setup.sh stops here (set -e); never report a clone that did not move as updated
				in.O.Warn("could not check out " + ref + " in " + home + " (kept as they are).")
				return
			}
		}
		in.O.OK("external skills: " + ref + " (" + home + ")")
	default:
		_ = os.MkdirAll(filepath.Dir(home), 0o755)
		log, err := git("clone", "--quiet", "--depth", "1", "--branch", ref, repo, home)
		if err != nil {
			showTail(in.O.err, log)
			in.O.Warn("could not clone " + repo + " " + ref + ": agents start without wayfinder, to-tickets, to-spec, ...")
			return
		}
		_ = os.WriteFile(mark, nil, 0o644)
		in.O.OK("external skills: cloned " + ref + " into " + home)
	}
}

// RoleSkills runs the repository's scripts/link-role-skills.sh, as setup.sh
// does (through the Runner, its output kept for a failure's tail).
func (in *Installer) RoleSkills(ctx context.Context) {
	if !in.Opt.InstallSkills {
		return
	}
	script := filepath.Join(in.Repo, "scripts", "link-role-skills.sh")
	if in.Opt.DryRun {
		in.O.Info("[dry-run] would run scripts/link-role-skills.sh")
		return
	}
	if _, err := os.Stat(script); err != nil {
		return
	}
	in.O.Info("Linking role skills for AI clients...")
	if log, err := in.R.Run(ctx, Cmd{Name: script}); err != nil {
		showTail(in.O.err, log)
		in.O.Warn("Role skills linking failed; re-run scripts/link-role-skills.sh to see the full output.")
		return
	}
	in.O.OK("Role skills: linked successfully")
}
