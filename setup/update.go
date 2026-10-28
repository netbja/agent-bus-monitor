package setup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Update is setup.sh update: refuse a dirty tree, fast-forward, say what
// changed, then exec the pulled installer's install with the same flags: the
// pull may change the steps, so this running binary never installs (spec:
// "update: running the new installer, not the old one").
func (in *Installer) Update(ctx context.Context) int {
	o := in.O
	o.Step("Agent Bus Monitor — update")
	git := func(args ...string) (string, error) {
		out, err := in.R.Run(ctx, Cmd{Name: "git", Args: append([]string{"-C", in.Repo}, args...), Stdout: true})
		return strings.TrimRight(string(out), "\n"), err
	}
	if _, err := git("diff", "--quiet"); err != nil {
		o.Fatal("the repository has uncommitted changes; commit or stash them first")
		return 1
	}
	if _, err := git("diff", "--cached", "--quiet"); err != nil {
		o.Fatal("the repository has uncommitted changes; commit or stash them first")
		return 1
	}
	from, _ := git("describe", "--tags", "--always")
	fromRev, _ := git("rev-parse", "HEAD")
	if in.Opt.DryRun {
		o.Info("[dry-run] would run git pull --ff-only in " + in.Repo + ", then install")
		return 0
	}
	if out, err := in.R.Run(ctx, Cmd{Name: "git", Args: []string{"-C", in.Repo, "pull", "--ff-only", "--quiet"}}); err != nil {
		showTail(o.err, out)
		o.Fatal("git pull --ff-only failed (a diverged branch is never merged by setup)")
		return 1
	}
	to, _ := git("describe", "--tags", "--always")
	if rev, _ := git("rev-parse", "HEAD"); rev == fromRev {
		o.OK("already up to date (" + to + "); reinstalling")
	} else {
		o.OK("updated " + from + " -> " + to + " (see CHANGELOG.md)")
		tags, _ := git("tag", "--merged", "HEAD", "--no-merged", fromRev, "--sort=v:refname")
		for _, tag := range strings.Fields(tags) {
			if _, err := os.Stat(filepath.Join(in.Repo, "docs", "releases", tag+".md")); err == nil {
				o.Info(tag + ": docs/releases/" + tag + ".md")
			}
		}
		// A running agent keeps the skills it loaded at boot: say which changed and how master
		// finds the agents to tell (agent-bus-master skill, "Skill updates").
		files, _ := git("diff", "--name-only", fromRev, "HEAD", "--", "skills/")
		seen := map[string]bool{}
		var changed []string
		for _, f := range strings.Split(files, "\n") {
			parts := strings.SplitN(f, "/", 3)
			if len(parts) < 2 || seen[parts[1]] {
				continue
			}
			seen[parts[1]] = true
			changed = append(changed, parts[1])
		}
		sort.Strings(changed)
		if len(changed) > 0 {
			c := strings.Join(changed, " ")
			o.Info("skills changed: " + c)
			o.Info("agents to tell: agentbus roles skill-users " + c + " (then a wake=no directive to re-invoke them)")
		}
	}
	args := []string{"install", "--bin-dir", in.Opt.BinDir}
	for _, f := range []struct {
		on   bool
		flag string
	}{{in.Opt.InstallHerdr, "--install-herdr"}, {in.Opt.Force, "--force"}, {!in.Opt.StartDocker, "--no-docker"}, {!in.Opt.InstallSkills, "--no-skills"}, {in.Opt.Yes, "--yes"}} {
		if f.on {
			args = append(args, f.flag)
		}
	}
	script := in.Script
	if script == "" {
		script = filepath.Join(in.Repo, "setup.sh")
	}
	env := append(os.Environ(), "AGENTBUS_SETUP_FROM="+from)
	if err := in.Exec(script, args, env); err != nil {
		o.Fatal("could not run " + script + ": " + err.Error())
		return 1
	}
	return 0
}
