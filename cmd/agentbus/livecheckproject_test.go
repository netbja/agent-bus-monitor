package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/trust"
)

// liveTestProject is a liveProject in a temporary home, its commands
// recorded, its broker the disposable one.
func liveTestProject(t *testing.T, clients string) (*liveProject, *[][]string, *[][]string) {
	t.Helper()
	testredis.Configure(t)
	home := t.TempDir()
	env := map[string]string{"HOME": home, "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CONFIG_HOME": filepath.Join(home, "config"),
		"XDG_RUNTIME_DIR": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	p, err := newLiveProject(clients, getenv, []string{"PATH=/usr/bin:/bin", "HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	if !bus.ValidName(p.project) || !strings.HasPrefix(p.project, "lc") || len(p.project) != 8 {
		t.Fatalf("project %q", p.project)
	}
	var argvs, envs [][]string
	p.run = func(_ context.Context, argv, env []string, out io.Writer) error {
		argvs, envs = append(argvs, argv), append(envs, env)
		return nil
	}
	p.exe = "/opt/agent bus/agentbus"
	p.connect = func() (*redis.Client, error) { return bus.Connect("") }
	return p, &argvs, &envs
}

func TestLiveProjectUpWithFakeClients(t *testing.T) {
	p, argvs, envs := liveTestProject(t, "fake")
	ctx := context.Background()
	if err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Down(ctx) })
	want := []string{p.exe, "up", p.project, p.work, "--yes", "--no-missions", "--roles", "master,coder"}
	if len(*argvs) != 1 || !slices.Equal((*argvs)[0], want) {
		t.Fatalf("ran %q, want %q", *argvs, want)
	}
	env := (*envs)[0]
	bin := filepath.Join(p.dir, "bin")
	if v := envValue(env, "PATH"); v != bin+":/usr/bin:/bin" {
		t.Fatalf("PATH %q", v)
	}
	if envValue(env, config.EnvName("dispatch.self_repair")) != "on" {
		t.Fatal("self-repair not on")
	}
	shim, err := os.ReadFile(filepath.Join(bin, "claude"))
	if err != nil || string(shim) != "#!/bin/sh\nexec '/opt/agent bus/agentbus' fake-client claude \"$@\"\n" {
		t.Fatalf("shim %q %v", shim, err)
	}
	if st, _ := os.Stat(filepath.Join(bin, "claude")); st.Mode()&0o100 == 0 {
		t.Fatalf("shim mode %v", st.Mode())
	}
	cfg := envValue(env, "CLAUDE_CONFIG_DIR")
	if cfg == "" || !trust.ClaudeTrusted(filepath.Join(cfg, ".claude.json"), p.work) {
		t.Fatalf("the working folder is not trusted in %q", cfg)
	}
	if trust.Reader(p.getenv)("claude-code", p.work) {
		t.Fatal("the person's own Claude config was written")
	}
	if !strings.HasPrefix(p.dir, filepath.Join(p.getenv("XDG_STATE_HOME"), "agentbus", ".livecheck")) {
		t.Fatalf("dir %s", p.dir)
	}
}

func TestLiveProjectRealClientsNeedTheFolderTrusted(t *testing.T) {
	p, argvs, _ := liveTestProject(t, "real")
	err := p.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not trust "+p.work) || len(*argvs) != 0 {
		t.Fatalf("%v, ran %q", err, *argvs)
	}
	p.trusted = func(client, dir string) bool { return client == "claude-code" && dir == p.work }
	if err := p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Down(context.Background()) })
	if len(*argvs) != 1 {
		t.Fatalf("ran %q", *argvs)
	}
}

// The kill reaches only the process the holder described: a pid reused
// since is left alone.
func TestLiveProjectCrashOnlyTheHolder(t *testing.T) {
	p, _, _ := liveTestProject(t, "fake")
	var killed []int
	p.kill = func(pid int) error { killed = append(killed, pid); return nil }
	p.procStart = func(pid int) (uint64, error) { return 7, nil }
	if err := p.Crash(context.Background(), dispatch.ProcID{PID: 4242, Start: 6}); err == nil || len(killed) != 0 {
		t.Fatalf("%v killed %v", err, killed)
	}
	p.procStart = func(pid int) (uint64, error) { return 0, errors.New("no such process") }
	if err := p.Crash(context.Background(), dispatch.ProcID{PID: 4242, Start: 6}); err == nil || len(killed) != 0 {
		t.Fatalf("%v killed %v", err, killed)
	}
	p.procStart = func(pid int) (uint64, error) { return 6, nil }
	if err := p.Crash(context.Background(), dispatch.ProcID{PID: 4242, Start: 6}); err != nil || !slices.Equal(killed, []int{4242}) {
		t.Fatalf("%v killed %v", err, killed)
	}
}

// What the check reads of the bus: the agents' processes and records, a
// wake drained, the swap ledger after a mark, health holder.
func TestLiveProjectReadsTheBus(t *testing.T) {
	p, _, _ := liveTestProject(t, "fake")
	ctx := context.Background()
	if err := p.open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Down(ctx) })
	b := p.b
	if st, detail, err := p.HolderHealth(ctx); err != nil || st != "" || detail != "not measured yet" {
		t.Fatalf("%q %q %v", st, detail, err)
	}
	runDrain(ctx, b, "coder", "h", io.Discard, false) // the group exists, read up to now
	id, err := p.Wake(ctx, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if done, err := p.Drained(ctx, "coder", id); err != nil || done {
		t.Fatalf("drained before any read: %v %v", done, err)
	}
	if code := runDrain(ctx, b, "coder", "h", io.Discard, false); code != 0 {
		t.Fatalf("drain %d", code)
	}
	if done, err := p.Drained(ctx, "coder", id); err != nil || !done {
		t.Fatalf("not drained after the drain: %v %v", done, err)
	}
	if mark, err := p.SwapMark(ctx, "coder"); err != nil || mark != "" {
		t.Fatalf("%q %v", mark, err)
	}
	if err := b.LogSwap(ctx, bus.SwapEvent{Role: "coder", Event: bus.SwapBegin}); err != nil {
		t.Fatal(err)
	}
	mark, _ := p.SwapMark(ctx, "coder")
	for _, ev := range []string{bus.SwapRelaunched, bus.SwapDone} {
		if err := b.LogSwap(ctx, bus.SwapEvent{Role: "coder", Event: ev}); err != nil {
			t.Fatal(err)
		}
	}
	b.LogSwap(ctx, bus.SwapEvent{Role: "master", Event: bus.SwapFailed})
	evs, err := p.SwapsSince(ctx, "coder", mark)
	if err != nil || len(evs) != 2 || evs[0].Event != bus.SwapRelaunched || evs[1].Event != bus.SwapDone {
		t.Fatalf("%+v %v", evs, err)
	}
	as, err := p.Agents(ctx)
	if err != nil || len(as) != 2 || as["coder"].PID != 0 || as["coder"].Session != "" {
		t.Fatalf("%+v %v", as, err)
	}
}

// A service remove that fails leaves everything else: the units it left
// still need the project's keys, template and state (reviewer-codex); Down
// says how to finish.
func TestLiveProjectDownKeepsTheProjectWhenItsServicesStay(t *testing.T) {
	p, _, _ := liveTestProject(t, "fake")
	ctx := context.Background()
	if err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(rolecfg.ProjectsDir(p.getenv), p.project+".toml")
	os.MkdirAll(filepath.Dir(tpl), 0o700)
	os.WriteFile(tpl, []byte("x"), 0o600)
	if _, err := p.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "x"); err != nil {
		t.Fatal(err)
	}
	p.run = func(context.Context, []string, []string, io.Writer) error { return errors.New("exit status 1") }
	err := p.Down(ctx)
	if err == nil || !strings.Contains(err.Error(), "agentbus --project "+p.project+" service remove") {
		t.Fatalf("%v", err)
	}
	for _, f := range []string{tpl, p.dir} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s removed: %v", f, err)
		}
	}
	r, _ := bus.Connect("")
	defer r.Close()
	if keys, _ := r.Keys(ctx, p.project+":*").Result(); len(keys) == 0 {
		t.Fatal("broker keys removed")
	}
	p.run = func(context.Context, []string, []string, io.Writer) error { return nil }
	if err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}
}

// Down removes what up made and what the check wrote; the working folder
// stays.
func TestLiveProjectDown(t *testing.T) {
	p, argvs, _ := liveTestProject(t, "fake")
	ctx := context.Background()
	if err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(rolecfg.ProjectsDir(p.getenv), p.project+".toml")
	state := service.Paths{StateHome: p.getenv("XDG_STATE_HOME")}.StateDir(p.project)
	for _, f := range []string{tpl, filepath.Join(state, "run.json")} {
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte("x"), 0o600)
	}
	if _, err := p.b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", "x"); err != nil {
		t.Fatal(err)
	}
	if err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if got := (*argvs)[len(*argvs)-1]; !slices.Equal(got, []string{p.exe, "--project", p.project, "service", "remove"}) {
		t.Fatalf("ran %q", got)
	}
	for _, f := range []string{tpl, state, p.dir} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left: %v", f, err)
		}
	}
	if st, err := os.Stat(p.work); err != nil || !st.IsDir() {
		t.Fatalf("the working folder went: %v", err)
	}
	r, _ := bus.Connect("")
	defer r.Close()
	keys, _ := r.Keys(ctx, p.project+":*").Result()
	if len(keys) != 0 {
		t.Fatalf("broker keys left: %v", keys)
	}
}
