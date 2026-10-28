package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// uninstallWorld is a machine install set up from in.Repo: binaries, links,
// role skills, the key binding, the plugin record, setup's skills checkout
// and a running broker this checkout owns.
func uninstallWorld(t *testing.T) (*Installer, *bytes.Buffer, *fakeRunner, string) {
	t.Helper()
	in, out, f, home := pluginWorld(t, "")
	bin := in.Opt.BinDir
	skills := home + "/.local/share/agentbus/skills/mattpocock"
	for _, d := range []string{bin, in.Repo + "/scripts", in.Repo + "/skills/tdd", skills + "/.git", home + "/.claude/skills", home + "/.config/herdr", home + "/.local/state/agentbus"} {
		_ = os.MkdirAll(d, 0o755)
	}
	for _, b := range []string{"agentbus", "busmon"} {
		_ = os.WriteFile(bin+"/"+b, []byte("#!/bin/sh\n"), 0o755)
	}
	for _, s := range []string{"agent-launch", "agent-spawn"} {
		_ = os.WriteFile(in.Repo+"/scripts/"+s, []byte("#!/bin/sh\n"), 0o755)
		_ = os.Symlink(in.Repo+"/scripts/"+s, bin+"/"+s)
	}
	_ = os.Symlink(in.Repo+"/skills/tdd", home+"/.claude/skills/tdd")
	_ = os.WriteFile(skills+"/.git/agentbus-setup", nil, 0o644)
	_ = os.WriteFile(home+"/.config/herdr/config.toml", []byte("[ui]\n"+KeybindBlock+"\n"), 0o644)
	_ = os.WriteFile(home+"/.local/state/agentbus/plugin.path", []byte(in.Repo+"\n"), 0o644)
	f.paths = map[string]string{"herdr": "/b/herdr", "docker": "/b/docker"}
	f.cmds = map[string]fakeCmd{
		bin + "/agentbus service list":             {out: "demo dispatch running\ndemo missions running\n"},
		bin + "/agentbus service remove --all":     {},
		bin + "/agentbus version":                  {out: "agentbus version v1\nagentbus protocol 1\n"},
		bin + "/busmon --version":                  {out: "busmon version v1\n"},
		"herdr server reload-config":               {},
		"herdr plugin list --json":                 {out: listing(row(in.Repo, true))},
		"herdr plugin unlink netbja.agentbus":      {},
		"git -C " + skills + " status --porcelain": {},
		psRedis:                  {out: "running\n"},
		"docker compose version": {},
		inspectOwner:             {out: in.Repo + "\n"},
	}
	return in, out, f, home
}

func TestUninstallYes(t *testing.T) {
	in, out, f, home := uninstallWorld(t)
	bin := in.Opt.BinDir
	f.cmds["docker compose stop"] = fakeCmd{do: func(c Cmd) {
		if c.Dir != in.Repo {
			t.Errorf("compose stop in %q", c.Dir)
		}
	}}
	if code := in.Uninstall(context.Background(), &Prompter{Yes: true}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	skills := home + "/.local/share/agentbus/skills/mattpocock"
	cfg := home + "/.config/herdr/config.toml"
	want := "\n==> Agent Bus Monitor — uninstall\n" +
		"    ✓ removed 2 project services\n" +
		"    ✓ removed " + bin + "/agentbus\n" +
		"    ✓ removed " + bin + "/busmon\n" +
		"    ✓ removed " + bin + "/agent-launch\n" +
		"    ✓ removed " + bin + "/agent-spawn\n" +
		"    ✓ removed 1 role-skill links\n" +
		"    ✓ removed the key binding: prefix+up is now unbound (backup: " + cfg + ".bak.20261028090000)\n" +
		"    ✓ herdr plugin netbja.agentbus: unlinked\n" +
		"    ✓ removed " + skills + "\n" +
		"    ✓ Redis: stopped (data kept; ./setup.sh install starts it again)\n" +
		"    • The repository and its built copies (" + in.Repo + "/agentbus, busmon) are left in place.\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "[ui]\n" {
		t.Fatalf("config %q", b)
	}
	for _, p := range []string{bin + "/agentbus", bin + "/agent-launch", home + "/.claude/skills/tdd", skills, home + "/.local/state/agentbus/plugin.path"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left", p)
		}
	}
}

// The broker another checkout owns is neither stopped nor purged from this
// one (spec: Safety, layer 2): no compose verb is registered, so running one
// fails the test.
func TestUninstallNeverStopsAnotherCheckoutsBroker(t *testing.T) {
	for _, inspect := range []fakeCmd{{out: "/data/projects/agent-bus-monitor\n"}, {out: "\n"}, {err: errors.New("1"), out: "daemon down\n"}} {
		in, out, f, _ := uninstallWorld(t)
		in.Opt.Purge = true
		f.cmds[inspectOwner] = inspect
		if code := in.Uninstall(context.Background(), &Prompter{Yes: true}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out.String(), "    ! Redis broker: ") || strings.Contains(out.String(), "Redis: ") {
			t.Fatalf("%q", out.String())
		}
	}
	// Ours and --purge: down -v from this checkout.
	in, out, f, _ := uninstallWorld(t)
	in.Opt.Purge = true
	f.cmds["docker compose down -v"] = fakeCmd{}
	in.Uninstall(context.Background(), &Prompter{Yes: true})
	if !strings.Contains(out.String(), "    ✓ Redis: container and data removed\n") {
		t.Fatalf("%q", out.String())
	}
}

// Asked on a terminal: n keeps each thing; no terminal and no --yes stops
// before anything is removed.
func TestUninstallAsks(t *testing.T) {
	in, out, _, _ := uninstallWorld(t)
	var shown bytes.Buffer
	tty := fakeTTY{strings.NewReader("n\nn\nn\nn\nn\nn\nn\nn\nn\nn\nn\n"), &shown}
	in.Uninstall(context.Background(), &Prompter{TTY: tty})
	for _, l := range []string{"    • kept " + in.Opt.BinDir + "/agentbus\n", "    • kept " + in.Opt.BinDir + "/agent-spawn\n", "    • Redis: left as it is (running)\n"} {
		if !strings.Contains(out.String(), l) {
			t.Errorf("missing %q in %s", l, out.String())
		}
	}
	if !strings.Contains(shown.String(), "    ? stop and remove the 2 project services (dispatch/missions units; their data and logs are kept)? [Y/n] ") ||
		!strings.Contains(shown.String(), "    ? remove the Redis container AND its data (every project on the bus)? [y/N] ") {
		t.Fatalf("%q", shown.String())
	}
	in, _, _, _ = uninstallWorld(t)
	var errw bytes.Buffer
	in.O = NewOutput(&bytes.Buffer{}, &errw, false)
	if code := in.Uninstall(context.Background(), &Prompter{}); code != 1 || !strings.Contains(errw.String(), "no terminal to ask on") {
		t.Fatalf("%d %q", code, errw.String())
	}
	if _, err := os.Stat(in.Opt.BinDir + "/agentbus"); err != nil {
		t.Fatal("removed without asking")
	}
}

// A failed service removal stops everything else.
func TestUninstallStopsWhenServicesStay(t *testing.T) {
	in, _, f, _ := uninstallWorld(t)
	var errw bytes.Buffer
	in.O = NewOutput(&bytes.Buffer{}, &errw, false)
	f.cmds[in.Opt.BinDir+"/agentbus service remove --all"] = fakeCmd{err: errors.New("1"), out: "unit busy\n"}
	if code := in.Uninstall(context.Background(), &Prompter{Yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if errw.String() != "      unit busy\n\nerror: some project services could not be stopped or removed (agentbus service list); nothing else was removed\n" {
		t.Fatalf("%q", errw.String())
	}
	if _, err := os.Stat(in.Opt.BinDir + "/agentbus"); err != nil {
		t.Fatal("removed after the failure")
	}
}

// No terminal at a later question: setup stops there, as log_fatal exits,
// and prints nothing after the error.
func TestUninstallStopsAtTheFirstUnaskedQuestion(t *testing.T) {
	in, _, f, _ := uninstallWorld(t)
	var out, errw bytes.Buffer
	in.O = NewOutput(&out, &errw, false)
	f.cmds[in.Opt.BinDir+"/agentbus service list"] = fakeCmd{out: "no agentbus services installed\n"}
	_ = os.Remove(in.Opt.BinDir + "/agentbus")
	_ = os.Remove(in.Opt.BinDir + "/busmon")
	_ = os.Remove(in.Opt.BinDir + "/agent-launch")
	_ = os.Remove(in.Opt.BinDir + "/agent-spawn")
	if code := in.Uninstall(context.Background(), &Prompter{}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "\n==> Agent Bus Monitor — uninstall\n" || errw.String() != "\nerror: no terminal to ask on: re-run with --yes to accept the cautious defaults\n" {
		t.Fatalf("%q %q", out.String(), errw.String())
	}
}

// Only the broker is left to ask about: still nothing printed after the error.
func TestUninstallBrokerQuestionWithoutTerminal(t *testing.T) {
	in, _, f, home := uninstallWorld(t)
	var out, errw bytes.Buffer
	in.O = NewOutput(&out, &errw, false)
	f.cmds[in.Opt.BinDir+"/agentbus service list"] = fakeCmd{out: "no agentbus services installed\n"}
	for _, p := range []string{in.Opt.BinDir + "/agentbus", in.Opt.BinDir + "/busmon", in.Opt.BinDir + "/agent-launch", in.Opt.BinDir + "/agent-spawn",
		home + "/.claude/skills/tdd", home + "/.config/herdr/config.toml", home + "/.local/state/agentbus/plugin.path", home + "/.local/share/agentbus"} {
		_ = os.RemoveAll(p)
	}
	if code := in.Uninstall(context.Background(), &Prompter{}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "\n==> Agent Bus Monitor — uninstall\n" || strings.Count(errw.String(), "error:") != 1 {
		t.Fatalf("%q %q", out.String(), errw.String())
	}
}
