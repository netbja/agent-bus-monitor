package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
)

// fakeProbes answers the github check's commands from a table and records them.
type fakeProbes struct {
	mu       sync.Mutex
	remotes  string
	ssh      probeRun
	https    probeRun
	commands [][]string
	envs     [][]string
}

func (f *fakeProbes) run(_ context.Context, env []string, name string, args ...string) probeRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string{name}, args...))
	f.envs = append(f.envs, env)
	switch {
	case name == "git" && len(args) > 0 && args[0] == "config":
		return probeRun{ran: true, out: f.remotes}
	case name == "ssh":
		return f.ssh
	default:
		return f.https
	}
}

var (
	sshOK     = probeRun{ran: true, exit: 1, out: "Hi x! You've successfully authenticated, but GitHub does not provide shell access."}
	sshDown   = probeRun{ran: true, exit: 255, out: "ssh: connect to host github.com port 22: Connection timed out"}
	httpsOK   = probeRun{ran: true, exit: 0, out: "abc\tHEAD\n"}
	httpsDown = probeRun{ran: true, exit: 128, out: "fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com"}
)

// The probes run as the spec says: ssh in batch mode, git ls-remote on the
// remote's https URL with no credential helper and no prompt; the repository
// comes from origin first, else any GitHub remote.
func TestGitHubCheckRunsTheSpecProbes(t *testing.T) {
	f := &fakeProbes{remotes: "remote.fork.url git@github.com:me/fork.git\nremote.origin.url git@github.com:netbja/agent-bus-monitor.git\n", ssh: sshOK, https: httpsOK}
	c := &githubCheck{run: f.run, now: time.Now}
	out := c.outcome(context.Background())
	if out.Status != health.OK {
		t.Fatalf("status %s: %s", out.Status, out.Detail)
	}
	var sawSSH, sawHTTPS bool
	for i, cmd := range f.commands {
		line := strings.Join(cmd, " ")
		switch cmd[0] {
		case "ssh":
			sawSSH = line == "ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com"
		case "git":
			if cmd[len(cmd)-1] == "https://github.com/netbja/agent-bus-monitor" {
				sawHTTPS = line == "git -c credential.helper= -c core.askPass= ls-remote https://github.com/netbja/agent-bus-monitor"
				if !strings.Contains(strings.Join(f.envs[i], " "), "GIT_TERMINAL_PROMPT=0") {
					t.Errorf("ls-remote may prompt: env %v", f.envs[i])
				}
			}
		}
	}
	for i, cmd := range f.commands { // English messages, whatever the service's locale (foureyes)
		if !slices.Contains(f.envs[i], "LC_ALL=C") {
			t.Errorf("%v runs in the service's locale: env %v", cmd, f.envs[i])
		}
	}
	if !sawSSH || !sawHTTPS {
		t.Errorf("probes not run as specified: %v", f.commands)
	}
	f.remotes = "remote.mirror.url https://gitlab.com/x/y.git\nremote.up.url https://github.com/o/r.git\n"
	if out := c.outcome(context.Background()); !strings.Contains(out.Detail, "(o/r)") {
		t.Errorf("a GitHub remote other than origin is not used: %s", out.Detail)
	}
	f.remotes = "remote.origin.url https://gitlab.com/x/y.git\n"
	n := len(f.commands)
	if out := c.outcome(context.Background()); out.Status != health.Unknown || !strings.Contains(out.Detail, "no GitHub remote") {
		t.Errorf("no GitHub remote: %s %s", out.Status, out.Detail)
	}
	if len(f.commands) != n+1 {
		t.Errorf("probes ran without a GitHub remote: %v", f.commands[n:])
	}
}

// Both down is unknown for an hour, then a warn; one transport reaching
// GitHub again starts the hour over.
func TestGitHubCheckWarnsAfterAnHourOfNeither(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	f := &fakeProbes{remotes: "remote.origin.url git@github.com:o/r.git\n", ssh: sshDown, https: httpsDown}
	c := &githubCheck{run: f.run, now: func() time.Time { return now }}
	if out := c.outcome(context.Background()); out.Status != health.Unknown {
		t.Fatalf("first failure: %s", out.Status)
	}
	now = now.Add(59 * time.Minute)
	if out := c.outcome(context.Background()); out.Status != health.Unknown {
		t.Fatalf("59 minutes: %s", out.Status)
	}
	now = now.Add(time.Minute)
	if out := c.outcome(context.Background()); out.Status != health.Warn || !strings.Contains(out.Detail, "reachable by neither") {
		t.Fatalf("an hour: %s %s", out.Status, out.Detail)
	}
	f.https = httpsOK
	now = now.Add(time.Minute)
	if out := c.outcome(context.Background()); out.Status != health.Warn || !strings.Contains(out.Detail, "HTTPS only") {
		t.Fatalf("HTTPS back: %s %s", out.Status, out.Detail)
	}
	f.https = httpsDown
	now = now.Add(time.Minute)
	if out := c.outcome(context.Background()); out.Status != health.Unknown {
		t.Errorf("both down again: the hour did not start over: %s", out.Status)
	}
}

// A probe that cannot run is not judged; the detail never carries the
// remote's credentials.
func TestGitHubCheckNotRunAndNoCredential(t *testing.T) {
	f := &fakeProbes{remotes: "remote.origin.url https://user:secret-token@github.com/o/r.git\n", ssh: probeRun{}, https: httpsOK}
	c := &githubCheck{run: f.run, now: time.Now}
	out := c.outcome(context.Background())
	if out.Status != health.Unknown || !strings.Contains(out.Detail, "SSH not judged") {
		t.Errorf("ssh missing: %s %s", out.Status, out.Detail)
	}
	if strings.Contains(out.Detail, "secret-token") {
		t.Errorf("the remote's credential is in the detail: %s", out.Detail)
	}
	for _, cmd := range f.commands[1:] { // the probes, after the remote read
		if strings.Contains(strings.Join(cmd, " "), "secret-token") {
			t.Errorf("a probe carries the remote's credential: %v", cmd)
		}
	}
}

// The remote read with the real git: the exact pattern finds origin in a
// repository's config (reviewer-codex asked for the pattern to be pinned).
func TestGitHubCheckReadsTheRemoteWithRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:netbja/agent-bus-monitor.git"}, {"remote", "add", "other", "https://gitlab.com/x/y.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	t.Chdir(dir)
	c := &githubCheck{run: runProbe, now: time.Now}
	if got := c.repo(context.Background()); got != "netbja/agent-bus-monitor" {
		t.Errorf("repo() = %q with the real git config", got)
	}
}

// No probe can open a desktop prompt (the person's KDE ksshaskpass asked for
// a GitHub username every 15 minutes): the inherited askpass programs are
// removed and ssh is told never to use one, whatever the service's
// environment holds.
func TestProbeEnvironCarriesNoAskpass(t *testing.T) {
	base := []string{"HOME=/h", "SSH_ASKPASS=/usr/bin/ksshaskpass", "GIT_ASKPASS=/usr/bin/ksshaskpass",
		"SSH_ASKPASS_REQUIRE=force", "LC_ALL=fr_FR.UTF-8", "DISPLAY=:0"}
	env := probeEnviron(base, probeEnv)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "SSH_ASKPASS" || k == "GIT_ASKPASS" {
			t.Errorf("the probe environment keeps %s", kv)
		}
		if k == "SSH_ASKPASS_REQUIRE" && kv != "SSH_ASKPASS_REQUIRE=never" {
			t.Errorf("the probe environment keeps %s", kv)
		}
	}
	for _, want := range []string{"HOME=/h", "DISPLAY=:0", "SSH_ASKPASS_REQUIRE=never", "LC_ALL=C"} {
		if !slices.Contains(env, want) {
			t.Errorf("the probe environment lacks %s: %v", want, env)
		}
	}
	if i, j := slices.Index(env, "LC_ALL=fr_FR.UTF-8"), slices.Index(env, "LC_ALL=C"); i >= 0 && i > j {
		t.Errorf("the service's locale wins over LC_ALL=C: %v", env)
	}
}

// The real git against a server that asks for credentials, with an askpass
// program in every place git and ssh look: none is run, and the answer still
// counts as reached (a private repository is reachable, authentication unknown).
func TestGitHubHTTPSProbeNeverAsksWithRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	dir := t.TempDir()
	marker := filepath.Join(dir, "asked")
	askpass := filepath.Join(dir, "askpass")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho asked >> "+marker+"\necho x\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	if err := os.WriteFile(filepath.Join(dir, "gitconfig"), []byte("[core]\n\taskPass = "+askpass+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_ASKPASS", askpass)
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("SSH_ASKPASS_REQUIRE", "force")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env, args := httpsProbe(srv.URL + "/o/r")
	r := runProbe(ctx, env, "git", args...)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the probe ran an askpass program; output: %s", r.out)
	}
	if p := health.ClassifyHTTPS(r.ran, r.timedOut, r.exit, r.out); p.Result != health.ProbeReached {
		t.Fatalf("an answer asking for credentials is %v (%s): %s", p.Result, p.Note, r.out)
	}
}
