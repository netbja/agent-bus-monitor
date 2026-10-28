package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pluginWorld(t *testing.T, list string) (*Installer, *bytes.Buffer, *fakeRunner, string) {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	f := &fakeRunner{t: t, paths: map[string]string{"herdr": "/b/herdr"}, cmds: map[string]fakeCmd{
		"herdr plugin list --json": {out: list},
	}}
	var out bytes.Buffer
	env := map[string]string{"HOME": home}
	in := &Installer{R: f, O: NewOutput(&out, &bytes.Buffer{}, false), Opt: Options{BinDir: home + "/bin"}, Repo: repo, Tmp: t.TempDir(),
		Getenv: func(k string) string { return env[k] }, Now: func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.Local) }}
	return in, &out, f, home
}

func listing(rows ...string) string {
	return `{"result":{"plugins":[` + strings.Join(rows, ",") + `]}}`
}
func row(root string, enabled bool) string {
	e := "false"
	if enabled {
		e = "true"
	}
	return `{"plugin_id":"netbja.agentbus","enabled":` + e + `,"plugin_root":"` + root + `","version":"0.23"}`
}

func TestLinkPlugin(t *testing.T) {
	ctx := context.Background()
	// Not registered: linked and recorded, then the action checked.
	in, out, f, home := pluginWorld(t, listing())
	f.cmds["herdr plugin link "+in.Repo] = fakeCmd{}
	f.cmds["herdr plugin action list"] = fakeCmd{out: `{"result":{"actions":[{"action_id":"projects","plugin_id":"netbja.agentbus"}]}}`}
	if in.LinkPlugin(ctx) {
		t.Fatal("reported failed")
	}
	rec, _ := os.ReadFile(home + "/.local/state/agentbus/plugin.path")
	if string(rec) != in.Repo+"\n" || out.String() != "    ✓ herdr plugin netbja.agentbus: linked ("+in.Repo+")\n    ✓ herdr plugin netbja.agentbus: Projects picker action available\n" {
		t.Fatalf("%q %q", rec, out.String())
	}

	// Linked here and enabled; then disabled.
	in, out, f, _ = pluginWorld(t, listing(row("REPO", true)))
	f.cmds["herdr plugin list --json"] = fakeCmd{out: listing(row(in.Repo, true))}
	f.cmds["herdr plugin action list"] = fakeCmd{out: `{"error":{"code":"server_not_running"}}`}
	in.LinkPlugin(ctx)
	if want := "    ✓ herdr plugin netbja.agentbus: linked (" + in.Repo + ", version 0.23)\n" +
		"    • herdr plugin netbja.agentbus: Projects picker not checked (no herdr server running here, run ./setup.sh status inside herdr)\n"; out.String() != want {
		t.Fatalf("%q", out.String())
	}
	f.cmds["herdr plugin list --json"] = fakeCmd{out: listing(row(in.Repo, false))}
	out.Reset()
	in.LinkPlugin(ctx)
	if !strings.HasPrefix(out.String(), "    ! herdr plugin netbja.agentbus is disabled in herdr: left as it is (the Projects picker needs it enabled).\n") {
		t.Fatalf("%q", out.String())
	}

	// From another checkout: setup's recorded one followed, a foreign one left.
	in, out, f, home = pluginWorld(t, "")
	old := t.TempDir()
	f.cmds["herdr plugin list --json"] = fakeCmd{out: listing(row(old, true))}
	_ = os.MkdirAll(home+"/.local/state/agentbus", 0o755)
	_ = os.WriteFile(home+"/.local/state/agentbus/plugin.path", []byte(old+"\n"), 0o644)
	f.cmds["herdr plugin unlink netbja.agentbus"] = fakeCmd{}
	f.cmds["herdr plugin link "+in.Repo] = fakeCmd{err: errors.New("x"), out: "boom\n"}
	if !in.LinkPlugin(ctx) {
		t.Fatal("a failed relink reported linked")
	}
	if !strings.Contains(out.String(), "    • herdr plugin netbja.agentbus: unlinked from "+old+" (setup's previous checkout)\n") ||
		!strings.Contains(out.String(), "is now NOT linked (setup had just unlinked the previous checkout): re-run ./setup.sh install to link this one.") {
		t.Fatalf("%q", out.String())
	}
	in, out, f, _ = pluginWorld(t, listing(row("/elsewhere", true)))
	f.cmds["herdr plugin action list"] = fakeCmd{out: `{"result":{"actions":[]}}`}
	in.LinkPlugin(ctx)
	if !strings.Contains(out.String(), "is linked from /elsewhere, not from this checkout: left as it is") || !strings.Contains(out.String(), "herdr lists no netbja.agentbus.projects action") {
		t.Fatalf("%q", out.String())
	}

	// An unreadable listing; no herdr at all.
	in, out, _, _ = pluginWorld(t, "garbage")
	if !in.LinkPlugin(ctx) || out.String() != "    ! herdr plugin list --json could not be read (herdr 0.9 or newer is needed): netbja.agentbus not linked.\n" {
		t.Fatalf("%q", out.String())
	}
	in, out, f, _ = pluginWorld(t, "")
	delete(f.paths, "herdr")
	in.LinkPlugin(ctx)
	if out.String() != "    • herdr plugin netbja.agentbus: not linked (herdr is not on PATH; re-run setup once it is)\n" {
		t.Fatalf("%q", out.String())
	}
}

func TestKeyBinding(t *testing.T) {
	ctx := context.Background()
	in, out, f, home := pluginWorld(t, "")
	cfg := home + "/.config/herdr/config.toml"
	f.cmds["herdr server reload-config"] = fakeCmd{}
	// None yet: created, the server reloaded.
	in.KeyBinding(ctx, false)
	b, _ := os.ReadFile(cfg)
	if string(b) != KeybindBlock+"\n" || out.String() != "    ✓ herdr config: created "+cfg+" with the Projects picker shortcut (prefix+up)\n    ✓ herdr: running server reloaded its config (Ctrl+B then Up works now)\n" {
		t.Fatalf("%q %q", b, out.String())
	}
	out.Reset()
	in.KeyBinding(ctx, false)
	if out.String() != "    ✓ herdr config: Projects picker keybinding already configured\n" {
		t.Fatalf("%q", out.String())
	}
	// herdr-plus's block: swapped with a backup; edited: left.
	_ = os.WriteFile(cfg, []byte("[ui]\n"+OldKeybindBlock+"\n"), 0o644)
	out.Reset()
	in.KeyBinding(ctx, false)
	b, _ = os.ReadFile(cfg)
	backup := cfg + ".bak.20261028090000"
	if string(b) != "[ui]\n"+KeybindBlock+"\n" || !strings.Contains(out.String(), "now opens netbja.agentbus's (backup: "+backup+")") {
		t.Fatalf("%q %q", b, out.String())
	}
	if bb, _ := os.ReadFile(backup); string(bb) != "[ui]\n"+OldKeybindBlock+"\n" {
		t.Fatalf("backup %q", bb)
	}
	_ = os.WriteFile(cfg, []byte("[ui]"+strings.Replace(OldKeybindBlock, "herdr-plus: projects", "mine", 1)+"\n"), 0o644)
	out.Reset()
	in.KeyBinding(ctx, false)
	if !strings.Contains(out.String(), "was edited: left as it is (bind netbja.agentbus.projects by hand).") {
		t.Fatalf("%q", out.String())
	}
	// prefix+up taken; otherwise appended after the person's config.
	_ = os.WriteFile(cfg, []byte("[[keys.command]]\nkey = \"prefix+up\"\n"), 0o644)
	out.Reset()
	in.KeyBinding(ctx, false)
	if !strings.Contains(out.String(), "prefix+up is already bound in "+cfg) {
		t.Fatalf("%q", out.String())
	}
	_ = os.WriteFile(cfg, []byte("[ui]\n"), 0o644)
	out.Reset()
	in.KeyBinding(ctx, false)
	if b, _ := os.ReadFile(cfg); string(b) != "[ui]\n"+KeybindBlock+"\n" || !strings.Contains(out.String(), "added Projects picker shortcut (prefix+up) to "+cfg) {
		t.Fatalf("%q %q", b, out.String())
	}
	// The link failed: nothing written.
	_ = os.Remove(cfg)
	out.Reset()
	in.KeyBinding(ctx, true)
	if _, err := os.Stat(cfg); err == nil || out.String() != "    • herdr config: Projects picker key binding not written (the plugin is not linked)\n" {
		t.Fatalf("%q", out.String())
	}
	_ = filepath.Join
}

func TestCheckHerdr(t *testing.T) {
	ctx := context.Background()
	in, out, f, home := pluginWorld(t, "")
	f.cmds["herdr --version"] = fakeCmd{out: "herdr 0.9.2\n"}
	in.CheckHerdr(ctx)
	if out.String() != "    ✓ herdr: herdr 0.9.2 (/b/herdr)\n" {
		t.Fatalf("%q", out.String())
	}
	in, out, f, _ = pluginWorld(t, "")
	f.cmds["herdr --version"] = fakeCmd{err: errors.New("x")}
	in.CheckHerdr(ctx)
	if out.String() != "    ✓ herdr: installed (/b/herdr)\n" {
		t.Fatalf("%q", out.String())
	}

	// Not on PATH: a hint, a dry run, or the download then the run.
	in, out, f, _ = pluginWorld(t, "")
	delete(f.paths, "herdr")
	in.CheckHerdr(ctx)
	if out.String() != "    ! herdr is not on PATH. Install it from https://herdr.dev, or re-run with --install-herdr.\n" {
		t.Fatalf("%q", out.String())
	}
	in.Opt.InstallHerdr, in.Opt.DryRun = true, true
	out.Reset()
	in.CheckHerdr(ctx)
	if out.String() != "    • [dry-run] would download and run https://herdr.dev/install.sh\n" {
		t.Fatalf("%q", out.String())
	}
	in.Opt.DryRun = false
	home = in.Getenv("HOME")
	script := in.Tmp + "/herdr-install.sh"
	f.cmds["curl -fsSL https://herdr.dev/install.sh -o "+script] = fakeCmd{}
	f.cmds["sh "+script] = fakeCmd{do: func(c Cmd) {
		if !c.Attach {
			t.Error("the herdr installer runs on setup's terminal")
		}
		f.paths["herdr"] = home + "/.local/bin/herdr"
	}}
	var path string
	in.Setenv = func(k, v string) error { path = k + "=" + v; return nil }
	out.Reset()
	in.CheckHerdr(ctx)
	if !strings.HasPrefix(path, "PATH="+home+"/.local/bin:") || out.String() != "    • Downloading the herdr installer (https://herdr.dev/install.sh)...\n    ✓ herdr: installed successfully ("+home+"/.local/bin/herdr)\n" {
		t.Fatalf("%q %q", path, out.String())
	}
	delete(f.paths, "herdr")
	f.cmds["sh "+script] = fakeCmd{err: errors.New("x")}
	out.Reset()
	in.CheckHerdr(ctx)
	if !strings.HasSuffix(out.String(), "    ! Automatic herdr installation failed. Install manually: https://herdr.dev\n") {
		t.Fatalf("%q", out.String())
	}
}

func TestServicesHint(t *testing.T) {
	ctx := context.Background()
	in, out, f, _ := pluginWorld(t, "")
	ab := in.Opt.BinDir + "/agentbus"
	_ = os.MkdirAll(in.Opt.BinDir, 0o755)
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	f.cmds[ab+" service list"] = fakeCmd{out: "no agentbus services installed\n"}
	in.ServicesHint(ctx)
	if out.String() != "" {
		t.Fatalf("%q", out.String())
	}
	f.cmds[ab+" service list"] = fakeCmd{out: "demo dispatch running since 2h\ndemo missions stopped\n"}
	in.ServicesHint(ctx)
	if want := "    • project services keep running the previous agentbus until restarted:\n" +
		"    •   demo dispatch (running since 2h): agentbus --project demo service restart dispatch\n" +
		"    •   demo missions (stopped): agentbus --project demo service restart missions\n"; out.String() != want {
		t.Fatalf("%q", out.String())
	}
	in.Opt.DryRun = true
	out.Reset()
	in.ServicesHint(ctx)
	if out.String() != "" {
		t.Fatalf("%q", out.String())
	}
}

// setup.sh's same_dir: equal names match even when the directory is gone;
// otherwise both must exist and resolve to one directory.
func TestSameDir(t *testing.T) {
	d := t.TempDir()
	_ = os.Symlink(d, d+".link")
	for _, c := range []struct {
		a, b string
		want bool
	}{{"/gone/x", "/gone/x", true}, {"", "", false}, {d, d + ".link", true}, {d, "/gone/x", false}, {"/gone/x", "/gone/y", false}} {
		if got := sameDir(c.a, c.b); got != c.want {
			t.Errorf("sameDir(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// The checkout setup linked was moved away (its directory is gone): its
// registration is still setup's, so it is followed (reviewer-codex, step 3).
func TestLinkPluginFollowsAMovedCheckout(t *testing.T) {
	in, out, f, home := pluginWorld(t, "")
	old := filepath.Join(t.TempDir(), "moved-away")
	f.cmds["herdr plugin list --json"] = fakeCmd{out: listing(row(old, true))}
	_ = os.MkdirAll(home+"/.local/state/agentbus", 0o755)
	_ = os.WriteFile(home+"/.local/state/agentbus/plugin.path", []byte(old+"\n"), 0o644)
	f.cmds["herdr plugin unlink netbja.agentbus"] = fakeCmd{}
	f.cmds["herdr plugin link "+in.Repo] = fakeCmd{}
	f.cmds["herdr plugin action list"] = fakeCmd{out: `{"result":{"actions":[{"action_id":"projects","plugin_id":"netbja.agentbus"}]}}`}
	if in.LinkPlugin(context.Background()) {
		t.Fatalf("failed: %q", out.String())
	}
	if !strings.HasPrefix(out.String(), "    • herdr plugin netbja.agentbus: unlinked from "+old+" (setup's previous checkout)\n    ✓ herdr plugin netbja.agentbus: linked ("+in.Repo+")\n") {
		t.Fatalf("%q", out.String())
	}
}

// herdr's config is rewritten whole and atomically, through its symlink
// (a dotfiles link stays a link), keeping its mode (foureyes, step 3).
func TestKeyBindingWritesAtomicallyThroughALink(t *testing.T) {
	in, _, f, home := pluginWorld(t, "")
	f.cmds["herdr server reload-config"] = fakeCmd{}
	real := filepath.Join(t.TempDir(), "dotfiles-config.toml")
	_ = os.WriteFile(real, []byte("[ui]\n"+OldKeybindBlock+"\n"), 0o600)
	cfg := home + "/.config/herdr/config.toml"
	_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
	_ = os.Symlink(real, cfg)
	in.KeyBinding(context.Background(), false)
	if fi, err := os.Lstat(cfg); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	b, _ := os.ReadFile(real)
	fi, _ := os.Stat(real)
	if string(b) != "[ui]\n"+KeybindBlock+"\n" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%q %v", b, fi.Mode())
	}
	left, _ := filepath.Glob(filepath.Dir(real) + "/.*tmp*")
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	// Appended the same way.
	_ = os.WriteFile(real, []byte("[ui]\n"), 0o600)
	in.KeyBinding(context.Background(), false)
	if b, _ := os.ReadFile(real); string(b) != "[ui]\n"+KeybindBlock+"\n" {
		t.Fatalf("%q", b)
	}
	if fi, _ := os.Lstat(cfg); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced on append")
	}
}
