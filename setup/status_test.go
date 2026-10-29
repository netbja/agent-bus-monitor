package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

const psRedis = "docker ps -a --filter name=^agent-bus-redis$ --format {{.State}}"

// A machine with everything installed by this checkout: setup.sh's status
// lines, in its order.
func TestStatusAllInstalled(t *testing.T) {
	in, out, f, home := pluginWorld(t, "")
	in.Repo = home + "/repo"
	bin := in.Opt.BinDir
	skills := home + "/.local/share/agentbus/skills/mattpocock"
	for _, d := range []string{bin, in.Repo + "/scripts", in.Repo + "/skills/tdd", skills + "/.git", home + "/.claude/skills", home + "/.agents/skills", home + "/.config/herdr"} {
		_ = os.MkdirAll(d, 0o755)
	}
	for _, b := range []string{bin + "/agentbus", bin + "/busmon"} {
		_ = os.WriteFile(b, []byte("#!/bin/sh\n"), 0o755)
	}
	for _, s := range []string{"agent-launch", "agent-spawn"} {
		_ = os.WriteFile(in.Repo+"/scripts/"+s, []byte("#!/bin/sh\n"), 0o755)
		_ = os.Symlink(in.Repo+"/scripts/"+s, bin+"/"+s)
	}
	_ = os.Symlink(in.Repo+"/skills/tdd", home+"/.claude/skills/tdd")
	_ = os.Symlink(skills+"/x", home+"/.agents/skills/x")
	_ = os.Symlink("/elsewhere", home+"/.agents/skills/mine") // not ours
	_ = os.WriteFile(home+"/.config/herdr/config.toml", []byte(KeybindBlock+"\n"), 0o644)
	f.paths = map[string]string{"herdr": "/b/herdr", "docker": "/b/docker", "agentbus": bin + "/agentbus", "busmon": "/usr/bin/busmon"}
	f.cmds = map[string]fakeCmd{
		"git -C " + in.Repo + " describe --tags --always --dirty": {out: "v0.23.0-4-gabc\n"},
		"git -C " + in.Repo + " rev-parse --abbrev-ref HEAD":      {out: "main\n"},
		bin + "/agentbus version":                                 {out: "agentbus version v0.23.0\nagentbus protocol 1\n"},
		bin + "/busmon --version":                                 {out: "busmon version v0.23.0\n"},
		"git -C " + skills + " describe --tags --always":          {out: "v1.3.1\n"},
		"herdr plugin list --json":                                {out: listing(row(in.Repo, true))},
		"herdr plugin action list":                                {out: `{"result":{"actions":[{"action_id":"projects","plugin_id":"netbja.agentbus"}]}}`},
		bin + "/agentbus service list":                            {out: "demo dispatch running\n"},
		psRedis:                                                   {out: "running\n"},
		inspectOwner:                                              {out: in.Repo + "\n"},
	}
	if code := in.Status(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "\n==> Agent Bus Monitor — status (" + in.Repo + ")\n" +
		"    • repository: v0.23.0-4-gabc on main\n" +
		"    ✓ agentbus v0.23.0: " + bin + "/agentbus\n" +
		"    • agentbus: not installed at " + in.Repo + "/agentbus\n" +
		"    ✓ busmon v0.23.0: " + bin + "/busmon\n" +
		"    • busmon: not installed at " + in.Repo + "/busmon\n" +
		"    ! busmon on PATH is /usr/bin/busmon (shadows " + bin + "/busmon)\n" +
		"    ✓ agent-launch: " + bin + "/agent-launch -> " + in.Repo + "/scripts/agent-launch\n" +
		"    ✓ agent-spawn: " + bin + "/agent-spawn -> " + in.Repo + "/scripts/agent-spawn\n" +
		"    ✓ external skills: v1.3.1 at " + skills + "\n" +
		"    • role skill links: 2\n" +
		"    ✓ netbja.agentbus: linked from " + in.Repo + " (version 0.23)\n" +
		"    ✓ netbja.agentbus: Projects picker available (Ctrl+B then Up)\n" +
		"    ✓ herdr key binding: prefix+up -> netbja.agentbus.projects\n" +
		"    • project service: demo dispatch running\n" +
		"    ✓ Redis broker: running (agent-bus-redis, 127.0.0.1:6380)\n" +
		"    ! Redis password: the historical default (./setup.sh rotate-redis-password replaces it)\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// A bare machine: nothing installed, no herdr, no docker.
func TestStatusNothingInstalled(t *testing.T) {
	in, out, f, home := pluginWorld(t, "")
	f.paths = map[string]string{}
	f.cmds = map[string]fakeCmd{
		"git -C " + in.Repo + " describe --tags --always --dirty": {err: errors.New("128")},
		"git -C " + in.Repo + " rev-parse --abbrev-ref HEAD":      {err: errors.New("128")},
	}
	in.Status(context.Background())
	bin, skills := in.Opt.BinDir, home+"/.local/share/agentbus/skills/mattpocock"
	want := "\n==> Agent Bus Monitor — status (" + in.Repo + ")\n" +
		"    • repository: unknown on ?\n" +
		"    • agentbus: not installed at " + bin + "/agentbus\n" +
		"    • agentbus: not installed at " + in.Repo + "/agentbus\n" +
		"    • busmon: not installed at " + bin + "/busmon\n" +
		"    • busmon: not installed at " + in.Repo + "/busmon\n" +
		"    • agent-launch: not installed at " + bin + "/agent-launch\n" +
		"    • agent-spawn: not installed at " + bin + "/agent-spawn\n" +
		"    • external skills: not installed (" + skills + ")\n" +
		"    • role skill links: 0\n" +
		"    • herdr: not on PATH\n" +
		"    • herdr key binding: not added\n" +
		"    • project services: none\n" +
		"    • Redis broker: no container\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// The other branches: a foreign link, herdr-plus's leftovers, a stopped
// broker another checkout owns (the stated extra line), foreign launchers.
func TestStatusForeignThings(t *testing.T) {
	in, out, f, home := pluginWorld(t, "")
	bin := in.Opt.BinDir
	_ = os.MkdirAll(bin, 0o755)
	_ = os.Symlink("/mine/agent-launch", bin+"/agent-launch")
	_ = os.MkdirAll(home+"/.local/state/agentbus", 0o755)
	_ = os.WriteFile(home+"/.local/state/agentbus/herdr-plus.ref", []byte("v1\n"), 0o644)
	_ = os.MkdirAll(home+"/.config/herdr", 0o755)
	_ = os.WriteFile(home+"/.config/herdr/config.toml", []byte(OldKeybindBlock+"\n"), 0o644)
	f.paths = map[string]string{"herdr": "/b/herdr", "docker": "/b/docker"}
	f.cmds = map[string]fakeCmd{
		"git -C " + in.Repo + " describe --tags --always --dirty": {out: "abc\n"},
		"git -C " + in.Repo + " rev-parse --abbrev-ref HEAD":      {out: "x\n"},
		"herdr plugin list --json":                                {out: listing(row("/elsewhere", false))},
		"herdr plugin action list":                                {out: `{"error":{"code":"server_not_running"}}`},
		psRedis:                                                   {out: "exited\n"},
		inspectOwner:                                              {out: "/data/projects/agent-bus-monitor\n"},
	}
	in.Status(context.Background())
	for _, l := range []string{
		"    ! agent-launch: " + bin + "/agent-launch exists but is not this repository's link\n",
		"    ! netbja.agentbus: linked from /elsewhere, not from this checkout\n",
		"    • netbja.agentbus: Projects picker not checked (no herdr server running here)\n",
		"    • herdr-plus: installed by an older setup, no longer needed: herdr plugin uninstall cloudmanic.herdr-plus\n",
		"    • herdr key binding: still herdr-plus's (./setup.sh install moves it to netbja.agentbus)\n",
		"    • Redis broker: stopped (agent-bus-redis)\n    • Redis broker: belongs to /data/projects/agent-bus-monitor (start or stop it from there)\n",
	} {
		if !bytes.Contains(out.Bytes(), []byte(l)) {
			t.Errorf("missing %q in\n%s", l, out.String())
		}
	}
	_ = filepath.Join
}

// redis-password: status names where the clients' password comes from, never
// the password itself.
func TestStatusPasswordLine(t *testing.T) {
	cfg := t.TempDir()
	repo := t.TempDir()
	path := filepath.Join(cfg, "agentbus", "redis.env")
	for _, c := range []struct {
		env  map[string]string
		file string // "" none, "ok", "loose" (0644), "bad"
		want string
	}{
		{map[string]string{}, "", "! Redis password: the historical default (./setup.sh rotate-redis-password replaces it)"},
		{map[string]string{}, "ok", "✓ Redis password: " + path + " (0600)"},
		{map[string]string{}, "loose", "! Redis password: " + path + " is not 0600"},
		{map[string]string{}, "bad", "! Redis password: " + path + " cannot be read"},
		{map[string]string{"REDIS_PASSWORD": "exported-secret"}, "ok", "• Redis password: from the exported REDIS_PASSWORD (it overrides the password file)"},
		{map[string]string{"REDIS_URL": "redis://:url-secret@h:1"}, "ok", "• Redis password: from REDIS_URL"},
		{map[string]string{"dotenv": "1"}, "ok", "! Redis password: " + repo + "/.env sets REDIS_PASSWORD: compose starts the broker with it, while the clients read " + path + "; export the same value for the clients, or move it into the password file and remove that line"},
	} {
		_ = os.RemoveAll(filepath.Join(cfg, "agentbus"))
		switch c.file {
		case "ok", "loose":
			if err := bus.WritePassword(path, "file-secret"); err != nil {
				t.Fatal(err)
			}
			if c.file == "loose" {
				_ = os.Chmod(path, 0o644)
			}
		case "bad":
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, []byte("garbage\n"), 0o600)
		}
		var out bytes.Buffer
		c.env["XDG_CONFIG_HOME"] = cfg
		_ = os.Remove(filepath.Join(repo, ".env"))
		if c.env["dotenv"] != "" {
			_ = os.WriteFile(filepath.Join(repo, ".env"), []byte("REDIS_PASSWORD=dotenv-secret\n"), 0o600)
		}
		in := &Installer{O: NewOutput(&out, &out, false), Repo: repo, Getenv: func(k string) string { return c.env[k] }}
		in.passwordLine()
		if got := strings.TrimSpace(out.String()); got != c.want {
			t.Errorf("%v %q: %q, want %q", c.env, c.file, got, c.want)
		}
		if strings.Contains(out.String(), "secret") {
			t.Errorf("the line discloses a password: %q", out.String())
		}
	}
}
