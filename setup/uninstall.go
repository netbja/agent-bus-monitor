package setup

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// isOurBinary is setup.sh's is_our_binary: a binary that answers like
// agentbus or busmon.
func (in *Installer) isOurBinary(ctx context.Context, p string) bool {
	switch filepath.Base(p) {
	case "agentbus":
		out, _ := in.R.Run(ctx, Cmd{Name: p, Args: []string{"version"}, Stdout: true})
		return strings.Contains(string(out), "agentbus protocol")
	case "busmon":
		if out, _ := in.R.Run(ctx, Cmd{Name: p, Args: []string{"--version"}, Stdout: true}); strings.Contains(string(out), "busmon version") {
			return true
		}
		out, _ := in.R.Run(ctx, Cmd{Name: p, Args: []string{"--help"}})
		return strings.Contains(string(out), "-project")
	}
	return false
}

// Uninstall is setup.sh uninstall: every step asks (--yes takes the
// cautious default) and removes only what setup made. The broker is stopped
// or purged only when this checkout owns its container.
func (in *Installer) Uninstall(ctx context.Context, p *Prompter) int {
	o := in.O
	o.Step("Agent Bus Monitor — uninstall")
	abort := false
	ask := func(q string, def bool) bool {
		if abort {
			return false
		}
		yes, err := Ask(o, p, q, def)
		if err != nil {
			abort = true
		}
		return yes
	}
	// The project services first, while an agentbus is still there to remove them.
	if svcs := in.serviceLines(ctx); len(svcs) > 0 {
		n := strconv.Itoa(len(svcs))
		if ask("stop and remove the "+n+" project services (dispatch/missions units; their data and logs are kept)?", true) {
			ab := filepath.Join(in.Opt.BinDir, "agentbus")
			if !isExec(ab) {
				ab = filepath.Join(in.Repo, "agentbus")
			}
			if out, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"service", "remove", "--all"}}); err == nil {
				o.OK("removed " + n + " project services")
			} else {
				showTail(o.err, out)
				o.Fatal("some project services could not be stopped or removed (agentbus service list); nothing else was removed")
				return 1
			}
		}
		if abort {
			return 1
		}
	}
	remove := func(p string) {
		if ask("remove "+p+"?", true) {
			_ = os.Remove(p)
			o.OK("removed " + p)
		} else if !abort {
			o.Info("kept " + p)
		}
	}
	// Each step in setup.sh's order; a question with no terminal ends the run
	// there, as log_fatal does.
	for _, step := range []func(){
		func() {
			for _, name := range []string{"agentbus", "busmon"} {
				if b := filepath.Join(in.Opt.BinDir, name); !abort && isExec(b) && in.isOurBinary(ctx, b) {
					remove(b)
				}
			}
		},
		func() {
			for _, script := range []string{"agent-launch", "agent-spawn"} {
				if l := filepath.Join(in.Opt.BinDir, script); !abort && OurScriptLink(l, in.Repo) {
					remove(l)
				}
			}
		},
		func() {
			if links := in.OurSkillLinks(); len(links) > 0 && ask("remove the "+strconv.Itoa(len(links))+" role-skill links (symlinks only)?", true) {
				for _, l := range links {
					_ = os.Remove(l)
				}
				o.OK("removed " + strconv.Itoa(len(links)) + " role-skill links")
			}
		},
		func() { in.removeKeyBinding(ctx, ask) },
		func() { in.unlinkPlugin(ctx, ask) },
		func() { in.removeSkills(ctx, ask) },
		func() { in.stopBroker(ctx, ask, p) },
	} {
		if step(); abort {
			return 1
		}
	}
	o.Info("The repository and its built copies (" + in.Repo + "/agentbus, busmon) are left in place.")
	return 0
}

func (in *Installer) removeKeyBinding(ctx context.Context, ask func(string, bool) bool) {
	cfg := in.HerdrConfig()
	content, err := os.ReadFile(cfg)
	if err != nil || !strings.Contains(string(content), keybindMark) || !ask("remove the herdr key binding (prefix+up -> agent-bus projects)?", true) {
		return
	}
	// Drop the block install wrote, line for line, and the blank line before it.
	swapped, ok := KeybindSwap(KeybindBlock, "", string(content))
	if !ok {
		in.O.Warn("the key binding block in " + cfg + " was edited: left as it is (remove it by hand)")
		return
	}
	backup, err := in.backup(cfg)
	if err == nil {
		err = writeAtomic(cfg, []byte(swapped))
	}
	if err != nil {
		in.O.Warn("could not rewrite " + cfg + ": " + err.Error())
		return
	}
	in.O.OK("removed the key binding: prefix+up is now unbound (backup: " + backup + ")")
	if _, err := in.R.LookPath("herdr"); err == nil {
		_, _ = in.herdr(ctx, "server", "reload-config")
	}
}

// unlinkPlugin unlinks the registration only when herdr still has it from
// the checkout setup recorded.
func (in *Installer) unlinkPlugin(ctx context.Context, ask func(string, bool) bool) {
	recorded := in.recordedPlugin()
	if recorded == "" {
		return
	}
	var rows []PluginRow
	ok := false
	if _, err := in.R.LookPath("herdr"); err == nil {
		list, err := in.herdr(ctx, "plugin", "list", "--json")
		rows, ok = PluginRows(list)
		ok = ok && err == nil
	}
	r, _ := Ours(rows)
	switch {
	case !ok:
		in.O.Warn("herdr plugin list --json could not be read: " + PluginID + " left as it is")
	case r.Root == "":
		_ = os.Remove(in.PluginRecord())
	case !sameDir(r.Root, recorded):
		in.O.Info(PluginID + " is linked from " + r.Root + ", which setup did not link: kept")
	case ask("unlink the herdr plugin "+PluginID+" ("+r.Root+")?", true):
		if out, err := in.herdr(ctx, "plugin", "unlink", PluginID); err == nil {
			_ = os.Remove(in.PluginRecord())
			in.O.OK("herdr plugin " + PluginID + ": unlinked")
		} else {
			showTail(in.O.err, out)
			in.O.Warn("herdr plugin unlink " + PluginID + " failed: left as it is")
		}
	}
}

func (in *Installer) removeSkills(ctx context.Context, ask func(string, bool) bool) {
	home := SkillsHome(in.Getenv)
	if _, err := os.Stat(filepath.Join(home, ".git", "agentbus-setup")); err == nil {
		changes, err := in.R.Run(ctx, Cmd{Name: "git", Args: []string{"-C", home, "status", "--porcelain"}, Stdout: true})
		if err != nil || len(changes) > 0 {
			in.O.Warn(home + " has local changes, or git cannot read it: kept")
		} else if ask("remove the external skills folder "+home+"?", true) {
			_ = os.RemoveAll(home)
			in.O.OK("removed " + home)
		}
	} else if _, err := os.Lstat(home); err == nil {
		in.O.Info(home + " was not created by setup: kept")
	}
}

// stopBroker purges (--purge, or yes to the question) or stops the broker,
// from this checkout only (spec: Safety, layer 2).
func (in *Installer) stopBroker(ctx context.Context, ask func(string, bool) bool, p *Prompter) {
	state := in.RedisState(ctx)
	cc := ComposeCmd(ctx, in.R)
	if state == "absent" || cc == "" || !in.mayCompose(ctx, "left as it is.") {
		return
	}
	run := func(verb ...string) bool {
		name, args := composeArgs(cc, verb...)
		_, err := in.R.Run(ctx, Cmd{Name: name, Args: args, Dir: in.Repo})
		return err == nil
	}
	purge := in.Opt.Purge || (!p.Yes && ask("remove the Redis container AND its data (every project on the bus)?", false))
	switch {
	case p.Aborted():
	case purge:
		if run("down", "-v") {
			in.O.OK("Redis: container and data removed")
		} else {
			in.O.Warn("Redis: " + cc + " down -v failed")
		}
	case state == "running" && ask("stop the Redis broker (its data is kept)?", true):
		if run("stop") {
			in.O.OK("Redis: stopped (data kept; ./setup.sh install starts it again)")
		} else {
			in.O.Warn("Redis: " + cc + " stop failed")
		}
	case !p.Aborted():
		in.O.Info("Redis: left as it is (" + state + ")")
	}
}
