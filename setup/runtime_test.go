package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
)

// podmanEnv is broker.runtime=podman, with no config.toml.
func podmanEnv(t *testing.T) func(string) string {
	cfg := t.TempDir()
	return func(k string) string {
		switch k {
		case "AGENT_BUS_BROKER_RUNTIME":
			return "podman"
		case "XDG_CONFIG_HOME":
			return cfg
		}
		return ""
	}
}

// broker-hardening step 3: with broker.runtime=podman, preflight looks for
// podman and its compose, the plugin first, then podman-compose; no docker
// command runs (the fake runner fails on one).
func TestPreflightUnderPodman(t *testing.T) {
	ctx := context.Background()
	f := &fakeRunner{t: t, paths: map[string]string{"git": "/bin/git", "go": "/bin/go", "podman": "/bin/podman"}, cmds: map[string]fakeCmd{
		"git --version":          {out: "git version 2.45\n"},
		"go env GOVERSION":       {out: "go1.26.0\n"},
		"podman info":            {},
		"podman compose version": {},
	}}
	var out, errw bytes.Buffer
	_, d := Preflight(ctx, f, NewOutput(&out, &errw, false), Options{}, "1.26", "linux", "amd64", brokerrt.Podman)
	if !d.OK || d.Compose != "podman compose" || !strings.Contains(out.String(), "podman: daemon is running") {
		t.Fatalf("docker %+v:\n%s", d, out.String())
	}
	f.cmds["podman compose version"] = fakeCmd{err: errors.New("1")}
	f.paths["podman-compose"] = "/bin/podman-compose"
	if c := ComposeCmd(ctx, f, brokerrt.Podman); c != "podman-compose" {
		t.Fatalf("standalone %q", c)
	}
	for _, r := range f.ran {
		if strings.HasPrefix(r, "docker") {
			t.Fatalf("a docker command ran: %s", r)
		}
	}
	out.Reset()
	f.paths = map[string]string{"git": "/bin/git", "go": "/bin/go"}
	Preflight(ctx, f, NewOutput(&out, &errw, false), Options{}, "1.26", "linux", "amd64", brokerrt.Podman)
	if !strings.Contains(out.String(), "podman is not installed (broker.runtime=podman)") {
		t.Fatalf("no podman:\n%s", out.String())
	}
}

// The broker's owner, its volume, its state and its start: podman's
// commands, podman compose's start.
func TestInstallerBrokerUnderPodman(t *testing.T) {
	ctx := context.Background()
	in, out, _, f := redisWorld(t, false, true)
	in.Getenv = podmanEnv(t)
	f.cmds[strings.Replace(inspectOwner, "docker", "podman", 1)] = fakeCmd{err: errors.New("1"), out: "Error: no such object: agent-bus-redis\nNo such object\n"}
	f.cmds[strings.Replace(inspectVolume, "docker", "podman", 1)] = fakeCmd{err: errors.New("1"), out: "Error: no such volume agent-bus_redis-data\n"}
	f.cmds["podman compose up -d"] = fakeCmd{}
	in.Redis(ctx, Docker{OK: true, Compose: "podman compose"})
	if !strings.Contains(out.String(), "started successfully") {
		t.Fatalf("%s\nran %v", out.String(), f.ran)
	}
	in, out, _, _ = redisWorld(t)
	in.Getenv = podmanEnv(t)
	in.Redis(ctx, Docker{OK: true})
	if !strings.Contains(out.String(), "run 'podman compose up -d' manually") {
		t.Fatalf("%q", out.String())
	}
	f.paths["podman"] = "/bin/podman"
	f.cmds["podman ps -a --filter name=^agent-bus-redis$ --format {{.State}}"] = fakeCmd{out: "running\n"}
	in2 := &Installer{R: f, Getenv: podmanEnv(t)}
	if st := in2.RedisState(ctx); st != "running" {
		t.Fatalf("state %q", st)
	}
	for _, r := range f.ran {
		if strings.HasPrefix(r, "docker") {
			t.Fatalf("a docker command ran: %s", r)
		}
	}
}

// toPodman renames the fixture's docker commands and paths to podman's.
func toPodman(f *fakeRunner) {
	cmds := map[string]fakeCmd{}
	for k, v := range f.cmds {
		if rest, ok := strings.CutPrefix(k, "docker"); ok {
			k = "podman" + rest
		}
		cmds[k] = v
	}
	f.cmds = cmds
	if _, ok := f.paths["docker"]; ok {
		delete(f.paths, "docker")
		f.paths["podman"] = "/usr/bin/podman"
	}
}

// Uninstall stops this checkout's broker with podman compose.
func TestUninstallUnderPodman(t *testing.T) {
	in, out, f, _ := uninstallWorld(t)
	base := in.Getenv
	in.Getenv = func(k string) string {
		if k == "AGENT_BUS_BROKER_RUNTIME" {
			return "podman"
		}
		return base(k)
	}
	toPodman(f)
	f.cmds["podman compose stop"] = fakeCmd{}
	if code := in.Uninstall(context.Background(), &Prompter{Yes: true}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Redis: stopped") {
		t.Fatalf("%s\nran %v", out.String(), f.ran)
	}
	for _, r := range f.ran {
		if strings.HasPrefix(r, "docker") {
			t.Fatalf("a docker command ran: %s", r)
		}
	}
}

// The rotation recreates the broker with podman compose, and says so when
// that fails.
func TestRotateUnderPodman(t *testing.T) {
	ctx := context.Background()
	in, out, f, _, _ := rotateWorld(t, map[string]string{"AGENT_BUS_BROKER_RUNTIME": "podman"})
	toPodman(f)
	b := &fakeBroker{pw: bus.LegacyPassword}
	f.cmds["podman compose up -d --force-recreate redis"] = fakeCmd{err: errors.New("1")}
	if code := in.RotateRedisPassword(ctx, &Prompter{Yes: true}, b.dial); code != 1 || !strings.Contains(out.String(), "run podman compose up -d --force-recreate redis") {
		t.Fatalf("exit %d: %s\nran %v", code, out.String(), f.ran)
	}
	for _, r := range f.ran {
		if strings.HasPrefix(r, "docker") {
			t.Fatalf("a docker command ran: %s", r)
		}
	}
}

// broker.image reaches compose on every call that may create the container
// (broker-hardening step 3): the start and, above all, the rotation's
// recreate, which would otherwise bring a Valkey broker back as Redis.
func TestComposeCarriesTheImage(t *testing.T) {
	ctx := context.Background()
	image := func(c Cmd) string {
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "AGENT_BUS_BROKER_IMAGE="); ok {
				return v
			}
		}
		return ""
	}
	in, _, _, f := redisWorld(t, false, true)
	cfg := t.TempDir()
	in.Getenv = func(k string) string {
		return map[string]string{"AGENT_BUS_BROKER_IMAGE": "valkey/valkey:8", "XDG_CONFIG_HOME": cfg}[k]
	}
	f.cmds[inspectOwner] = fakeCmd{err: errors.New("1"), out: "No such object\n"}
	f.cmds[inspectVolume] = fakeCmd{err: errors.New("1"), out: "no such volume\n"}
	got := ""
	f.cmds["docker compose up -d"] = fakeCmd{do: func(c Cmd) { got = image(c) }}
	in.Redis(ctx, Docker{OK: true, Compose: "docker compose"})
	if got != "valkey/valkey:8" {
		t.Fatalf("start: image %q", got)
	}
	in2, _, f2, _, _ := rotateWorld(t, map[string]string{"AGENT_BUS_BROKER_IMAGE": "valkey/valkey:8"})
	got = ""
	f2.cmds["docker compose up -d --force-recreate redis"] = fakeCmd{do: func(c Cmd) { got = image(c) }}
	in2.RotateRedisPassword(ctx, &Prompter{Yes: true}, (&fakeBroker{pw: bus.LegacyPassword}).dial)
	if got != "valkey/valkey:8" {
		t.Fatalf("the rotation's recreate: image %q", got)
	}
	in3, _, _, f3 := redisWorld(t, false, true)
	in3.Getenv = func(k string) string { return map[string]string{"XDG_CONFIG_HOME": t.TempDir()}[k] }
	f3.cmds[inspectOwner] = fakeCmd{err: errors.New("1"), out: "No such object\n"}
	f3.cmds[inspectVolume] = fakeCmd{err: errors.New("1"), out: "no such volume\n"}
	f3.cmds["docker compose up -d"] = fakeCmd{do: func(c Cmd) { got = image(c) }}
	in3.Redis(ctx, Docker{OK: true, Compose: "docker compose"})
	if got != "redis:8-alpine" {
		t.Fatalf("default: image %q", got)
	}
}
