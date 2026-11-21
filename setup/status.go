package setup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

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
	prefixes := []string{in.Repo + "/skills/", SkillsHome(in.Getenv) + "/"}
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
	bin := in.runtime().Bin()
	if _, err := in.R.LookPath(bin); err != nil {
		return "absent"
	}
	out, _ := in.R.Run(ctx, Cmd{Name: bin, Args: []string{"ps", "-a", "--filter", "name=^" + RedisContainer + "$", "--format", "{{.State}}"}, Stdout: true})
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
	for _, script := range []string{"agent-launch"} {
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
		in.passwordLine()
		in.aclLine(ctx, true)
	case "stopped":
		o.Info("Redis broker: stopped (" + RedisContainer + ")")
		in.ownerLine(ctx)
		in.passwordLine()
		in.aclLine(ctx, false)
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

// passwordLine says where the clients' password comes from (redis-password),
// never what it is.
func (in *Installer) passwordLine() {
	path := bus.PasswordFile(in.Getenv)
	if in.Repo != "" && in.Getenv("REDIS_PASSWORD") == "" && setsKey(filepath.Join(in.Repo, ".env"), "REDIS_PASSWORD") {
		// compose starts the broker with it; the clients read the file or the
		// default (reviewer-codex).
		clients := "the historical default"
		if _, err := os.Lstat(path); path != "" && err == nil {
			clients = path
		}
		in.O.Warn("Redis password: " + filepath.Join(in.Repo, ".env") + " sets REDIS_PASSWORD: compose starts the broker with it, while the clients read " + clients + "; export the same value for the clients, or move it into the password file and remove that line")
		return
	}
	switch {
	case in.Getenv("REDIS_URL") != "":
		in.O.Info("Redis password: from REDIS_URL")
	case in.Getenv("REDIS_PASSWORD") != "":
		in.O.Info("Redis password: from the exported REDIS_PASSWORD (it overrides the password file)")
	case path == "":
		in.O.Info("Redis password: unknown (no HOME)")
	default:
		st, err := os.Lstat(path)
		_, rerr := bus.ReadPassword(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			in.O.Warn("Redis password: the historical default (./setup.sh rotate-redis-password replaces it)")
		case err != nil || rerr != nil:
			in.O.Warn("Redis password: " + path + " cannot be read")
		case st.Mode().Perm() != 0o600:
			in.O.Warn("Redis password: " + path + " is not 0600")
		default:
			in.O.OK("Redis password: " + path + " (0600)")
		}
	}
}

// aclLine is broker.acl beside what the broker's data volume holds (the
// compose file loads /data/users.acl when it is there): each disagreement
// locks the clients out, so it is a warning. A stopped broker is not probed.
func (in *Installer) aclLine(ctx context.Context, running bool) {
	set := config.Machine(in.Getenv).String("broker.acl")
	if set != "on" {
		set = "off"
	}
	if !running {
		in.O.Info("broker ACL: broker.acl is " + set + " (the broker is stopped: its users.acl is not checked)")
		return
	}
	out, err := in.R.Run(ctx, Cmd{Name: in.runtime().Bin(), Args: []string{"exec", RedisContainer, "sh", "-c", "if [ -f /data/users.acl ]; then echo yes; else echo no; fi"}, Stdout: true})
	has := strings.TrimSpace(string(out))
	switch {
	case err != nil || (has != "yes" && has != "no"):
		in.O.Warn("broker ACL: broker.acl is " + set + "; users.acl in the broker's data volume could not be checked")
	case set == "on" && has == "yes":
		in.O.OK("broker ACL: on (users.acl in the broker's data volume, a password file per user beside " + bus.PasswordFile(in.Getenv) + ")")
	case set == "off" && has == "no":
		in.O.Info("broker ACL: off")
	case set == "on":
		in.O.Warn("broker ACL: broker.acl is on, but the broker's data volume holds no users.acl: the clients log in as users the broker does not have. Run ./setup.sh acl on, or set broker.acl back to off")
	default:
		in.O.Warn("broker ACL: broker.acl is off, but the broker's data volume holds users.acl: the clients log in as its default user, which it refuses. Run ./setup.sh acl off")
	}
}

// shadowed warns when name on PATH is not want.
func (in *Installer) shadowed(name, want string) {
	if found, err := in.R.LookPath(name); err == nil && found != "" && found != want {
		in.O.Warn(name + " on PATH is " + found + " (shadows " + want + ")")
	}
}
