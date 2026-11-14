package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// The helper process: this test binary as `agentbus --project demo holder
// --env <file>`, in a child, so the holder's subreaper and reaper never run in
// the test process (they would reap other tests' children).
func TestHolderHelperProcess(t *testing.T) {
	if os.Getenv("AGENTBUS_HOLDER_HELPER") != "1" {
		t.Skip("helper process")
	}
	os.Exit(mainHolder("demo", os.Args[len(os.Args)-2:], os.Stderr))
}

// A holder started by a service manager has a minimal PATH; it loads the
// project's env file (as `service run` does) and launches through it: the
// launcher is found from the env file's AGENTBUS_REPO, the client from the
// env file's PATH, and the agent runs in the unit's working directory.
func TestHolderLaunchesThroughTheEnvFile(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a holder process")
	}
	tmp := t.TempDir()
	repo, clientDir, work := filepath.Join(tmp, "repo"), filepath.Join(tmp, "clients"), filepath.Join(tmp, "work")
	for _, d := range []string{filepath.Join(repo, "scripts"), clientDir, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeExe := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "roles.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	writeExe(filepath.Join(repo, "scripts", "bootstrap"), "#!/bin/sh\nexit 1\n") // how repoDir knows a checkout
	writeExe(filepath.Join(repo, "scripts", "agent-launch"), "#!/bin/sh\nexec fake-client \"$@\"\n")
	mark := filepath.Join(tmp, "mark")
	writeExe(filepath.Join(clientDir, "fake-client"), "#!/bin/sh\necho \"$* in $(pwd) roles=$ROLES_TOML\" > \"$HOLDER_TEST_MARK\"\nexec sleep 30\n")

	home := filepath.Join(tmp, "home")
	run, err := os.MkdirTemp("", "hb") // short: a socket path fits in 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(tmp, "env", "demo.env")
	if _, err := service.WriteEnvFile(envFile, map[string]string{
		"PATH":             "/usr/bin:/bin:" + clientDir,
		"AGENTBUS_REPO":    repo,
		"HOLDER_TEST_MARK": mark,
	}); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHolderHelperProcess$", "--", "--env", envFile)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_RUNTIME_DIR=" + run, "AGENTBUS_HOLDER_HELPER=1"}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	sock, err := holder.SocketPath(func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return run
		}
		return ""
	}, "demo", service.BuildKey(holder.BuildID()))
	if err != nil {
		t.Fatal(err)
	}
	var cl *holder.Client
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if cl, err = holder.Dial(sock, "test"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no holder at %s: %v\n%s", sock, err, out.String())
		}
	}
	defer cl.Close()
	if _, err := cl.Call(holder.Start{Role: "coder"}); err != nil {
		t.Fatalf("start: %v\n%s", err, out.String())
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(mark)
		got := strings.TrimSpace(string(b))
		if got != "" {
			realWork, _ := filepath.EvalSymlinks(work)
			want := "coder demo in " + realWork + " roles=" + filepath.Join(repo, "roles.toml")
			if r, err := filepath.EvalSymlinks(filepath.Join(repo, "roles.toml")); err == nil {
				want = "coder demo in " + realWork + " roles=" + r
			}
			if got != want {
				t.Fatalf("the client ran as %q, want %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client never ran (not found through the env file's PATH?)\n%s", out.String())
		}
	}
}

// The holder refuses to serve without its env file: a service whose PATH
// cannot find a client would start agents that die at once.
func TestHolderRefusesAMissingEnvFile(t *testing.T) {
	var out strings.Builder
	t.Setenv("HOME", t.TempDir())
	if code := mainHolder("demo", []string{"--env", filepath.Join(t.TempDir(), "none.env")}, &out); code != 0 {
		t.Fatalf("exit %d: an invalid configuration must not be restarted", code)
	}
	if !strings.Contains(out.String(), "invalid configuration") {
		t.Fatalf("no reason given: %q", out.String())
	}
}
