package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
)

const (
	keybindMark    = "# agentbus Projects picker: prefix (ctrl+b) then Up"
	oldKeybindMark = "# herdr-plus Projects picker: prefix (ctrl+b) then Up"
)

// state is setup.sh's XDG_STATE_HOME, else ~/.local/state.
func (in *Installer) state() string {
	if s := in.Getenv("XDG_STATE_HOME"); s != "" {
		return s
	}
	return filepath.Join(in.Getenv("HOME"), ".local", "state")
}

// PluginRecord is where setup records the checkout it linked.
func (in *Installer) PluginRecord() string {
	return filepath.Join(in.state(), "agentbus", "plugin.path")
}

// HerdrConfig is setup.sh's HERDR_CONFIG.
func (in *Installer) HerdrConfig() string {
	if p := in.Getenv("HERDR_CONFIG_PATH"); p != "" {
		return p
	}
	cfg := in.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(in.Getenv("HOME"), ".config")
	}
	return filepath.Join(cfg, "herdr", "config.toml")
}

// recordedPlugin is the first line of the plugin record, "" without one.
func (in *Installer) recordedPlugin() string {
	b, err := os.ReadFile(in.PluginRecord())
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return line
}

func (in *Installer) herdr(ctx context.Context, args ...string) ([]byte, error) {
	return in.R.Run(ctx, Cmd{Name: "herdr", Args: args})
}

// link is setup.sh's link_plugin; it reports whether the link failed.
func (in *Installer) link(ctx context.Context, relink bool) bool {
	if in.Opt.DryRun {
		in.O.Info("[dry-run] would run: herdr plugin link " + in.Repo)
		return false
	}
	out, err := in.herdr(ctx, "plugin", "link", in.Repo)
	if err == nil {
		rec := in.PluginRecord()
		if os.MkdirAll(filepath.Dir(rec), 0o755) != nil || os.WriteFile(rec, []byte(in.Repo+"\n"), 0o644) != nil {
			in.O.Warn("could not record the plugin path in " + rec + ": setup will not follow this checkout if it moves (it will not unlink the registration either).")
		}
		in.O.OK("herdr plugin " + PluginID + ": linked (" + in.Repo + ")")
		return false
	}
	showTail(in.O.err, out)
	in.O.Warn("herdr plugin link " + in.Repo + " failed: the Projects picker is not available.")
	if relink {
		in.O.Warn(PluginID + " is now NOT linked (setup had just unlinked the previous checkout): re-run ./setup.sh install to link this one.")
	}
	return true
}

// LinkPlugin links this checkout as the herdr plugin: only a registration
// setup recorded is ever unlinked; a disabled or foreign one is reported and
// left. It reports whether the link failed, which keeps the key binding
// unwritten.
func (in *Installer) LinkPlugin(ctx context.Context) (failed bool) {
	if _, err := in.R.LookPath("herdr"); err != nil {
		in.O.Info("herdr plugin " + PluginID + ": not linked (herdr is not on PATH; re-run setup once it is)")
		return false
	}
	list, err := in.herdr(ctx, "plugin", "list", "--json")
	rows, ok := PluginRows(list)
	if err != nil {
		ok = false
	}
	r, found := Ours(rows)
	recorded := in.recordedPlugin()
	switch {
	case !ok:
		in.O.Warn("herdr plugin list --json could not be read (herdr 0.9 or newer is needed): " + PluginID + " not linked.")
		return true
	case !found:
		failed = in.link(ctx, false)
	case sameDir(r.Root, in.Repo):
		if r.Enabled {
			in.O.OK("herdr plugin " + PluginID + ": linked (" + r.Root + ", version " + r.Version + ")")
		} else {
			in.O.Warn("herdr plugin " + PluginID + " is disabled in herdr: left as it is (the Projects picker needs it enabled).")
		}
	case recorded != "" && sameDir(r.Root, recorded):
		// The checkout setup linked has moved here: follow it.
		if in.Opt.DryRun {
			in.O.Info("[dry-run] would run: herdr plugin unlink " + PluginID + " (" + r.Root + "), then herdr plugin link " + in.Repo)
		} else if out, err := in.herdr(ctx, "plugin", "unlink", PluginID); err == nil {
			in.O.Info("herdr plugin " + PluginID + ": unlinked from " + r.Root + " (setup's previous checkout)")
			_ = os.Remove(in.PluginRecord())
			failed = in.link(ctx, true)
		} else {
			showTail(in.O.err, out)
			in.O.Warn("herdr plugin unlink " + PluginID + " failed: still linked from " + r.Root + ".")
			failed = true
		}
	default:
		in.O.Warn("herdr plugin " + PluginID + " is linked from " + r.Root + ", not from this checkout: left as it is (herdr plugin unlink " + PluginID + ", then re-run setup, to use this one).")
	}
	// What Ctrl+B Up actually needs: the plugin's projects action, as herdr sees it.
	if !in.Opt.DryRun && !failed {
		out, _ := in.herdr(ctx, "plugin", "action", "list")
		switch pluginActionState(string(out)) {
		case "ok":
			in.O.OK("herdr plugin " + PluginID + ": Projects picker action available")
		case "noserver":
			in.O.Info("herdr plugin " + PluginID + ": Projects picker not checked (no herdr server running here, run ./setup.sh status inside herdr)")
		default:
			in.O.Warn("herdr lists no " + PluginID + ".projects action (check: herdr plugin action list).")
		}
	}
	return failed
}

// backup copies path to path.bak.<stamp> keeping its mode and times (cp -p).
func (in *Installer) backup(path string) (string, error) {
	dst := path + ".bak." + in.Now().Format("20060102150405")
	b, err := os.ReadFile(path)
	if err != nil {
		return dst, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return dst, err
	}
	if err := os.WriteFile(dst, b, fi.Mode().Perm()); err != nil {
		return dst, err
	}
	_ = os.Chmod(dst, fi.Mode().Perm())
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return dst, nil
}

// writeAtomic replaces path's content whole: a temp file beside the file
// path resolves to, renamed over it, so a crash leaves the old content or the
// new one, a symlinked config stays a link, and the mode is kept. A file the
// person cannot write (made read-only) is refused, as a direct write would be.
func writeAtomic(path string, data []byte) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return err
	}
	w, err := os.OpenFile(real, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_ = w.Close()
	f, err := os.CreateTemp(filepath.Dir(real), "."+filepath.Base(real)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, fi.Mode().Perm())
	}
	if err == nil {
		err = os.Rename(tmp, real)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// KeyBinding writes the Projects picker key binding into herdr's config,
// idempotently: never when the link failed, and setup's old herdr-plus block
// is replaced only when unedited. A running herdr is told to reload.
func (in *Installer) KeyBinding(ctx context.Context, linkFailed bool) {
	cfg := in.HerdrConfig()
	content, readErr := os.ReadFile(cfg)
	exists := readErr == nil
	if _, err := os.Stat(cfg); err == nil {
		exists = true
	}
	written := false
	switch {
	case linkFailed:
		in.O.Info("herdr config: Projects picker key binding not written (the plugin is not linked)")
	// herdr runs with a partial config.toml, so a machine where it never ran gets one holding the binding.
	case !exists && in.Opt.DryRun:
		in.O.Info("[dry-run] would create " + cfg + " with the Projects picker key binding (prefix+up)")
	case !exists:
		_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
		if err := os.WriteFile(cfg, []byte(KeybindBlock+"\n"), 0o644); err != nil {
			in.O.Warn("could not write " + cfg + ": " + err.Error())
			break
		}
		in.O.OK("herdr config: created " + cfg + " with the Projects picker shortcut (prefix+up)")
		written = true
	case bytes.Contains(content, []byte(keybindMark)):
		in.O.OK("herdr config: Projects picker keybinding already configured")
	case bytes.Contains(content, []byte(oldKeybindMark)):
		swapped, ok := KeybindSwap(OldKeybindBlock, KeybindBlock, string(content))
		switch {
		case !ok:
			in.O.Warn("herdr config: the herdr-plus Projects picker block in " + cfg + " was edited: left as it is (bind " + PluginID + ".projects by hand).")
		case in.Opt.DryRun:
			in.O.Info("[dry-run] would replace the herdr-plus Projects picker binding (prefix+up) in " + cfg + " with " + PluginID + "'s")
		default:
			b, err := in.backup(cfg)
			if err == nil {
				err = writeAtomic(cfg, []byte(swapped))
			}
			if err != nil {
				in.O.Warn("could not rewrite " + cfg + ": " + err.Error())
				break
			}
			in.O.OK("herdr config: the herdr-plus Projects picker binding (prefix+up) now opens " + PluginID + "'s (backup: " + b + ")")
			written = true
		}
	case bytes.Contains(content, []byte(`"prefix+up"`)) || bytes.Contains(content, []byte(`'prefix+up'`)):
		in.O.Warn("herdr config: prefix+up is already bound in " + cfg + "; bind " + PluginID + ".projects to another key by hand.")
	case in.Opt.DryRun:
		in.O.Info("[dry-run] would add the Projects picker key binding (prefix+up) to " + cfg)
	default:
		b, err := in.backup(cfg)
		if err == nil {
			err = writeAtomic(cfg, append(content, []byte(KeybindBlock+"\n")...))
		}
		if err != nil {
			in.O.Warn("could not append to " + cfg + ": " + err.Error())
			break
		}
		in.O.OK("herdr config: added Projects picker shortcut (prefix+up) to " + cfg + " (backup: " + b + ")")
		written = true
	}
	// A herdr that is already running reads a new binding only when told to.
	if written {
		if _, err := in.R.LookPath("herdr"); err == nil {
			if _, err := in.herdr(ctx, "server", "reload-config"); err == nil {
				in.O.OK("herdr: running server reloaded its config (Ctrl+B then Up works now)")
			} else {
				in.O.Info("herdr: the binding applies when herdr next starts (or run: herdr server reload-config)")
			}
		}
	}
}

// CheckHerdr is step 2/5: herdr on PATH, or installed from herdr.dev when
// --install-herdr asks (downloaded first, run second: a truncated download
// is never executed half-way).
func (in *Installer) CheckHerdr(ctx context.Context) {
	if p, err := in.R.LookPath("herdr"); err == nil {
		v, err := in.R.Run(ctx, Cmd{Name: "herdr", Args: []string{"--version"}, Stdout: true})
		ver := strings.TrimRight(string(v), "\n")
		if err != nil || ver == "" {
			ver = "installed"
		}
		in.O.OK("herdr: " + ver + " (" + p + ")")
		return
	}
	switch {
	case !in.Opt.InstallHerdr:
		in.O.Warn("herdr is not on PATH. Install it from https://herdr.dev, or re-run with --install-herdr.")
		return
	case in.Opt.DryRun:
		in.O.Info("[dry-run] would download and run https://herdr.dev/install.sh")
		return
	}
	in.O.Info("Downloading the herdr installer (https://herdr.dev/install.sh)...")
	script := filepath.Join(in.Tmp, "herdr-install.sh")
	if _, err := in.R.Run(ctx, Cmd{Name: "curl", Args: []string{"-fsSL", "https://herdr.dev/install.sh", "-o", script}, Attach: true}); err != nil {
		in.O.Warn("Automatic herdr installation failed. Install manually: https://herdr.dev")
		return
	}
	if _, err := in.R.Run(ctx, Cmd{Name: "sh", Args: []string{script}, Attach: true}); err != nil {
		in.O.Warn("Automatic herdr installation failed. Install manually: https://herdr.dev")
		return
	}
	if in.Setenv != nil {
		_ = in.Setenv("PATH", filepath.Join(in.Getenv("HOME"), ".local", "bin")+":"+in.Getenv("PATH"))
	}
	if p, err := in.R.LookPath("herdr"); err == nil {
		in.O.OK("herdr: installed successfully (" + p + ")")
	} else {
		in.O.Warn("herdr installed to ~/.local/bin; ensure it is added to your PATH.")
	}
}

// serviceLines is setup.sh's project_services: one "<project> <kind>
// <state>" line per agentbus user service, read through the agentbus
// installed, else the checkout's; none when there is no service.
func (in *Installer) serviceLines(ctx context.Context) []string {
	for _, ab := range []string{filepath.Join(in.Opt.BinDir, "agentbus"), filepath.Join(in.Repo, "agentbus")} {
		if !isExec(ab) {
			continue
		}
		out, _ := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"service", "list"}, Stdout: true})
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if !strings.HasPrefix(l, "no ") {
				lines = append(lines, l)
			}
		}
		if strings.TrimSpace(strings.Join(lines, "")) == "" {
			return nil
		}
		return lines
	}
	return nil
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// ServicesHint names the running project services, which keep the binary
// they started with, and how to move each on.
func (in *Installer) ServicesHint(ctx context.Context) {
	if in.Opt.DryRun {
		return
	}
	lines := in.serviceLines(ctx)
	if len(lines) == 0 {
		return
	}
	in.O.Info("project services keep running the previous agentbus until restarted:")
	for _, l := range lines {
		f := strings.Fields(l)
		for len(f) < 2 {
			f = append(f, "")
		}
		p, k := f[0], f[1]
		rest := strings.Join(f[2:], " ")
		in.O.Info("  " + p + " " + k + " (" + rest + "): agentbus --project " + p + " service restart " + k)
	}
}
