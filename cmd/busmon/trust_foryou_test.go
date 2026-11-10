package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// first-run-trust step 2: an agent of a gated client, running on this host,
// whose pane shows a prompt in a folder that client does not trust yet: the
// folder its dialog is about. Anything else ("", no line): a trusted folder,
// a client that is not gated, a process that is not on this host (another
// host's agents, through a tunnel or a remote broker: their clients' files
// are not here), a process that is not the one recorded, no template.
//
// pid and start time alone prove no host (reviewer-codex): start is ticks
// since this kernel's boot, so a remote agent may match a local process by
// chance. The local process must also be this project's agent of that name,
// as its launcher exported it (AGENT_BUS_PROJECT, AGENT_BUS_AGENT); an
// environment that cannot be read is no proof.
func TestTrustFolderOf(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub")
	local := func(pid int) (uint64, error) {
		if pid == 4242 {
			return 7, nil
		}
		return 0, errors.New("no such process")
	}
	envOf := map[int]map[string]string{4242: {"AGENT_BUS_PROJECT": "demo", "AGENT_BUS_AGENT": "master"}}
	procEnv := func(pid int) (func(string) string, error) {
		e, ok := envOf[pid]
		if !ok {
			return nil, errors.New("permission denied")
		}
		return func(k string) string { return e[k] }, nil
	}
	deps := func(dir string, ok bool, trusted bool) trustDeps {
		return trustDeps{project: "demo", procStart: local, procEnv: procEnv, workingDir: func() (string, bool) { return dir, ok },
			trusted: func(client, d string) bool {
				if d != dir {
					t.Errorf("trust read for %q, want the working_dir %q", d, dir)
				}
				return trusted
			}}
	}
	cases := []struct {
		name   string
		d      trustDeps
		client string
		pid    int
		start  uint64
		want   string
	}{
		{"claude, untrusted", deps(repo, true, false), "claude-code", 4242, 7, repo},
		{"codex, untrusted", deps(repo, true, false), "codex", 4242, 7, repo},
		{"keyed on the repository root", deps(sub, true, false), "claude-code", 4242, 7, repo},
		{"trusted", deps(repo, true, true), "claude-code", 4242, 7, ""},
		{"not gated", deps(repo, true, false), "kimi-cli", 4242, 7, ""},
		{"not on this host", deps(repo, true, false), "claude-code", 999, 7, ""},
		{"another process", deps(repo, true, false), "claude-code", 4242, 8, ""},
		{"no pid", deps(repo, true, false), "claude-code", 0, 0, ""},
		{"no template", deps("", false, false), "claude-code", 4242, 7, ""},
	}
	for _, c := range cases {
		if got := c.d.folder("master", c.client, c.pid, c.start); got != c.want {
			t.Errorf("%s: folder = %q, want %q", c.name, got, c.want)
		}
	}
	d := deps(repo, true, false)
	d.project = "elsewhere"
	if got := d.folder("master", "claude-code", 4242, 7); got != "" {
		t.Errorf("another project's process: folder = %q", got)
	}
	d.project = "demo"
	if got := d.folder("coder", "claude-code", 4242, 7); got != "" {
		t.Errorf("another agent's process: folder = %q", got)
	}
	delete(envOf, 4242)
	if got := d.folder("master", "claude-code", 4242, 7); got != "" {
		t.Errorf("an unreadable environment: folder = %q", got)
	}
}

// The project's working_dir is its template's (projlist), ~ and $VARS
// expanded as agentbus up does; no template, or none for the project: none.
func TestTrustWorkingDir(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "agentbus", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("name = \"demo\"\nworking_dir = \"~/src/demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string { return map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": cfg}[k] }
	if got, ok := templateWorkingDir("demo", env)(); !ok || got != "/h/src/demo" {
		t.Fatalf("demo: %q %v", got, ok)
	}
	if got, ok := templateWorkingDir("other", env)(); ok {
		t.Fatalf("a project with no template: %q", got)
	}
}

// Through the poll: the prompt row says which folder and what to choose; the
// folder is read only for an agent showing a prompt; with no prompt, or no
// reader (nil), the row is today's.
func TestTrustLineThroughThePoll(t *testing.T) {
	now := time.Now().UnixMilli()
	f := &fakeReader{calls: map[string]int{},
		snaps: map[string]bus.AgentSnapshot{
			"master": {State: "working", TS: 1, Pane: "w2:p2", PID: 4242, PIDStart: 7, Client: "claude-code"},
			"coder":  {State: "working", TS: 1, Pane: "w2:p3", PID: 4343, PIDStart: 9, Client: "codex"}},
		observed: map[string]bus.Observation{"master": {State: "blocked", At: now, Since: now - 60000, PID: 4242}}}
	var asked []string
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true},
		trustOf: func(agent, client string, pid int, start uint64) string {
			asked = append(asked, agent+":"+client)
			return "/w/repo"
		}}
	pollOnce(context.Background(), f, st, 1)
	if strings.Join(asked, ",") != "master:claude-code" {
		t.Fatalf("trust read for %q: only master shows a prompt", asked)
	}
	row := promptRow(t, takeSnapshot(st, time.Now()), "master")
	if !strings.Contains(row, "Claude Code asks whether you trust /w/repo") || !strings.Contains(row, `choose "Yes, I trust this folder"`) {
		t.Fatalf("master's prompt row: %q", row)
	}

	f.observed = map[string]bus.Observation{"master": {State: "idle", At: now, PID: 4242}}
	asked = nil
	pollOnce(context.Background(), f, st, 1)
	if st.agents["master"].trustFolder != "" || len(asked) != 0 {
		t.Fatalf("no prompt: folder %q, reads %q", st.agents["master"].trustFolder, asked)
	}

	f.observed = map[string]bus.Observation{"master": {State: "blocked", At: now, Since: now, PID: 4242}}
	st.trustOf = nil
	pollOnce(context.Background(), f, st, 1)
	if row := promptRow(t, takeSnapshot(st, time.Now()), "master"); row != "pane prompt" {
		t.Fatalf("no reader: row %q", row)
	}
}

func promptRow(t *testing.T, s snapshot, agent string) string {
	t.Helper()
	for _, it := range forYou(s, nil) {
		if it.Agent == agent && strings.HasPrefix(it.Key, "prompt:") {
			return it.Text
		}
	}
	t.Fatalf("no prompt row for %s", agent)
	return ""
}

// procEnviron reads a live process's launcher names from /proc, and keeps no
// other variable; a process that does not exist is an error (no line).
func TestProcEnviron(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc: no trust line on this system")
	}
	cmd := exec.Command("sleep", "30")
	cmd.Env = []string{"AGENT_BUS_PROJECT=demo", "AGENT_BUS_AGENT=master", "SECRET_TOKEN=x"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	exe, _ := exec.LookPath("sleep")
	exe, _ = filepath.EvalSymlinks(exe)
	for deadline := time.Now().Add(5 * time.Second); ; { // until the child has exec'd sleep: before, /proc shows the parent's environment
		if got, _ := os.Readlink(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "exe")); got == exe || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	env, err := procEnviron(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if env("AGENT_BUS_PROJECT") != "demo" || env("AGENT_BUS_AGENT") != "master" || env("SECRET_TOKEN") != "" {
		t.Fatalf("project %q agent %q secret %q", env("AGENT_BUS_PROJECT"), env("AGENT_BUS_AGENT"), env("SECRET_TOKEN"))
	}
	if _, err := procEnviron(1 << 30); err == nil {
		t.Fatal("no error for a process that does not exist")
	}
}
