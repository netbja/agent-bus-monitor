package setup

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// herdrPlusState is where setup before v0.20 recorded the herdr-plus it
// installed; only read, to say it is no longer needed.
func (in *Installer) herdrPlusState() string {
	return filepath.Join(in.state(), "agentbus", "herdr-plus.ref")
}

// OurSkillLinks is setup.sh's our_skill_links: the role-skill symlinks
// install made (into this checkout's skills, the external skills folder or
// the historical ~/Tools checkout) in SKILLS_DEST, ~/.claude/skills and
// ~/.agents/skills. Never a real directory.
func (in *Installer) OurSkillLinks() []string {
	home := in.Getenv("HOME")
	var dirs []string
	if d := in.Getenv("SKILLS_DEST"); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills"))
	prefixes := []string{in.Repo + "/skills/", SkillsHome(in.Getenv) + "/", home + "/Tools/herdr-plugins/skills/"}
	var links []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.Type()&os.ModeSymlink == 0 {
				continue
			}
			link := filepath.Join(dir, e.Name())
			target, err := os.Readlink(link)
			if err != nil {
				continue
			}
			for _, p := range prefixes {
				if strings.HasPrefix(target, p) && len(target) > len(p) {
					links = append(links, link)
					break
				}
			}
		}
	}
	return links
}

// RedisState is setup.sh's redis_state: running, stopped or absent.
func (in *Installer) RedisState(ctx context.Context) string {
	if _, err := in.R.LookPath("docker"); err != nil {
		return "absent"
	}
	out, _ := in.R.Run(ctx, Cmd{Name: "docker", Args: []string{"ps", "-a", "--filter", "name=^" + RedisContainer + "$", "--format", "{{.State}}"}, Stdout: true})
	st, _, _ := strings.Cut(string(out), "\n")
	switch st {
	case "running":
		return "running"
	case "":
		return "absent"
	}
	return "stopped"
}

func (in *Installer) out(ctx context.Context, name string, args ...string) (string, bool) {
	b, err := in.R.Run(ctx, Cmd{Name: name, Args: args, Stdout: true})
	return strings.TrimRight(string(b), "\n"), err == nil
}

// versionOf is the version a binary prints after prefix, "" when none.
func versionOf(out, prefix string) string {
	var v []string
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(l, prefix); ok {
			v = append(v, s)
		}
	}
	return strings.Join(v, "\n")
}

// Status is setup.sh status: what is installed and from where, read-only.
func (in *Installer) Status(ctx context.Context) int {
	o := in.O
	o.Step("Agent Bus Monitor — status (" + in.Repo + ")")
	rev, ok := in.out(ctx, "git", "-C", in.Repo, "describe", "--tags", "--always", "--dirty")
	if !ok {
		rev = "unknown"
	}
	branch, ok := in.out(ctx, "git", "-C", in.Repo, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		branch = "?"
	}
	o.Info("repository: " + rev + " on " + branch)
	for _, name := range []string{"agentbus", "busmon"} {
		for _, p := range []string{filepath.Join(in.Opt.BinDir, name), filepath.Join(in.Repo, name)} {
			if !isExec(p) {
				o.Info(name + ": not installed at " + p)
				continue
			}
			var v string
			if name == "agentbus" {
				s, _ := in.out(ctx, p, "version")
				v = versionOf(s, "agentbus version ")
			} else {
				s, _ := in.out(ctx, p, "--version")
				v = versionOf(s, "busmon version ")
			}
			if v == "" {
				v = "?"
			}
			o.OK(name + " " + v + ": " + p)
		}
		in.shadowed(name, filepath.Join(in.Opt.BinDir, name))
	}
	for _, script := range []string{"agent-launch", "agent-spawn"} {
		p := filepath.Join(in.Opt.BinDir, script)
		_, lerr := os.Lstat(p)
		switch {
		case OurScriptLink(p, in.Repo):
			t, _ := os.Readlink(p)
			o.OK(script + ": " + p + " -> " + t)
		case lerr == nil:
			o.Warn(script + ": " + p + " exists but is not this repository's link")
		default:
			o.Info(script + ": not installed at " + p)
		}
		in.shadowed(script, p)
	}
	home := SkillsHome(in.Getenv)
	if isDirPath(filepath.Join(home, ".git")) {
		d, _ := in.out(ctx, "git", "-C", home, "describe", "--tags", "--always")
		o.OK("external skills: " + d + " at " + home)
	} else {
		o.Info("external skills: not installed (" + home + ")")
	}
	o.Info("role skill links: " + strconv.Itoa(len(in.OurSkillLinks())))
	in.pluginStatus(ctx)
	if _, err := os.Stat(in.herdrPlusState()); err == nil {
		o.Info("herdr-plus: installed by an older setup, no longer needed: herdr plugin uninstall cloudmanic.herdr-plus")
	}
	cfg, _ := os.ReadFile(in.HerdrConfig())
	switch {
	case strings.Contains(string(cfg), keybindMark):
		o.OK("herdr key binding: prefix+up -> " + PluginID + ".projects")
	case strings.Contains(string(cfg), oldKeybindMark):
		o.Info("herdr key binding: still herdr-plus's (./setup.sh install moves it to " + PluginID + ")")
	default:
		o.Info("herdr key binding: not added")
	}
	if lines := in.serviceLines(ctx); len(lines) > 0 {
		for _, l := range lines {
			o.Info("project service: " + l)
		}
	} else {
		o.Info("project services: none")
	}
	switch in.RedisState(ctx) {
	case "running":
		o.OK("Redis broker: running (" + RedisContainer + ", 127.0.0.1:6380)")
		in.ownerLine(ctx)
	case "stopped":
		o.Info("Redis broker: stopped (" + RedisContainer + ")")
		in.ownerLine(ctx)
	default:
		o.Info("Redis broker: no container")
	}
	return 0
}

// ownerLine is the stated extra line: the broker another checkout owns.
func (in *Installer) ownerLine(ctx context.Context) {
	ow, err := in.BrokerOwner(ctx)
	switch {
	case err != nil || !ow.Exists || ow.Ours:
	case ow.Dir == "":
		in.O.Info("Redis broker: belongs to no checkout (no compose label)")
	default:
		in.O.Info("Redis broker: belongs to " + ow.Dir + " (start or stop it from there)")
	}
}

// shadowed warns when name on PATH is not want.
func (in *Installer) shadowed(name, want string) {
	if found, err := in.R.LookPath(name); err == nil && found != "" && found != want {
		in.O.Warn(name + " on PATH is " + found + " (shadows " + want + ")")
	}
}

func (in *Installer) pluginStatus(ctx context.Context) {
	o := in.O
	if _, err := in.R.LookPath("herdr"); err != nil {
		o.Info("herdr: not on PATH")
		return
	}
	list, err := in.herdr(ctx, "plugin", "list", "--json")
	rows, ok := PluginRows(list)
	if err != nil || !ok {
		o.Warn("herdr plugin list --json could not be read: the " + PluginID + " registration is unknown")
		return
	}
	r, found := Ours(rows)
	if !found {
		o.Info(PluginID + ": not linked into herdr (./setup.sh install)")
		return
	}
	switch {
	case !sameDir(r.Root, in.Repo):
		o.Warn(PluginID + ": linked from " + r.Root + ", not from this checkout")
	case !r.Enabled:
		o.Warn(PluginID + ": linked from " + r.Root + " but disabled in herdr")
	default:
		o.OK(PluginID + ": linked from " + r.Root + " (version " + r.Version + ")")
	}
	out, _ := in.herdr(ctx, "plugin", "action", "list")
	switch pluginActionState(string(out)) {
	case "ok":
		o.OK(PluginID + ": Projects picker available (Ctrl+B then Up)")
	case "noserver":
		o.Info(PluginID + ": Projects picker not checked (no herdr server running here)")
	default:
		o.Warn(PluginID + ": registered but its projects action is missing (check: herdr plugin action list)")
	}
}
