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

// holderWorld is a checkout (bootstrap, agent-launch, roles.toml), a client
// directory reachable only through the env file's PATH, and the env file.
type holderWorld struct {
	tmp, repo, envFile, run string
	out                     *strings.Builder
	cmd                     *exec.Cmd
}

func newHolderWorld(t *testing.T) *holderWorld {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a holder process")
	}
	tmp := t.TempDir()
	w := &holderWorld{tmp: tmp, repo: filepath.Join(tmp, "repo"), envFile: filepath.Join(tmp, "env", "demo.env")}
	if err := os.MkdirAll(filepath.Join(w.repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.repo, "roles.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	writeExe(t, filepath.Join(w.repo, "scripts", "bootstrap"), "#!/bin/sh\nexit 1\n") // how repoDir knows a checkout
	writeExe(t, filepath.Join(w.repo, "scripts", "agent-launch"), "#!/bin/sh\nexec fake-client \"$@\"\n")
	run, err := os.MkdirTemp("", "hb") // short: a socket path fits in 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	w.run = run
	return w
}

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// client makes a client directory whose fake-client writes what it saw
// (args, directory, manifest, a tag) to $HOLDER_TEST_MARK, then stays.
func (w *holderWorld) client(t *testing.T, tag string) string {
	dir := filepath.Join(w.tmp, "clients-"+tag)
	writeExe(t, filepath.Join(dir, "fake-client"),
		"#!/bin/sh\necho \""+tag+" $* in $(pwd) roles=$ROLES_TOML\" > \"$HOLDER_TEST_MARK\"\nexec sleep 30\n")
	return dir
}

func (w *holderWorld) writeEnv(t *testing.T, env map[string]string) {
	t.Helper()
	if _, err := service.WriteEnvFile(w.envFile, env); err != nil {
		t.Fatal(err)
	}
}

// start runs the holder as a service manager would: a minimal PATH, the
// unit's working directory.
func (w *holderWorld) start(t *testing.T, cwd string) *holder.Client {
	t.Helper()
	w.out = &strings.Builder{}
	w.cmd = exec.Command(os.Args[0], "-test.run=^TestHolderHelperProcess$", "--", "--env", w.envFile)
	w.cmd.Dir = cwd
	w.cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + filepath.Join(w.tmp, "home"), "XDG_RUNTIME_DIR=" + w.run, "AGENTBUS_HOLDER_HELPER=1"}
	w.cmd.Stdout, w.cmd.Stderr = w.out, w.out
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd := w.cmd
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
			return w.run
		}
		return ""
	}, "demo", service.BuildKey(holder.BuildID()))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		cl, err := holder.Dial(sock, "test")
		if err == nil {
			t.Cleanup(func() { cl.Close() })
			return cl
		}
		if time.Now().After(deadline) {
			t.Fatalf("no holder at %s: %v\n%s", sock, err, w.out.String())
		}
	}
}

// mark waits for a client's line in path.
func (w *holderWorld) mark(t *testing.T, path string) string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client never wrote %s (not found through the env file's PATH?)\n%s", path, w.out.String())
		}
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A holder started by a service manager has a minimal PATH; it loads the
// project's env file (as `service run` does) and launches through it: the
// launcher is found from the env file's holder repo, the client from the
// env file's PATH, and the agent runs in the unit's working directory.
func TestHolderLaunchesThroughTheEnvFile(t *testing.T) {
	w := newHolderWorld(t)
	work := filepath.Join(w.tmp, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(w.tmp, "mark")
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin:" + w.client(t, "a"), service.HolderRepoEnv: w.repo, "HOLDER_TEST_MARK": mark})
	cl := w.start(t, work)
	if _, err := cl.Call(holder.Start{Role: "coder"}); err != nil {
		t.Fatalf("start: %v\n%s", err, w.out.String())
	}
	want := "a coder demo in " + realPath(t, work) + " roles=" + realPath(t, filepath.Join(w.repo, "roles.toml"))
	if got := w.mark(t, mark); got != want {
		t.Fatalf("the client ran as %q, want %q", got, want)
	}
}

// up may change the env file and the working directory while this build's
// holder runs (it is never restarted: that would kill its agents). Each Start
// reads its launch context afresh: a new terminal gets the new PATH, the new
// variables and the new directory; one already running is left as it is
// (reviewer-codex).
func TestHolderStartReadsTheLaunchContextAfresh(t *testing.T) {
	w := newHolderWorld(t)
	work1, work2 := filepath.Join(w.tmp, "work1"), filepath.Join(w.tmp, "work2")
	for _, d := range []string{work1, work2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mark1, mark2 := filepath.Join(w.tmp, "mark1"), filepath.Join(w.tmp, "mark2")
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin:" + w.client(t, "old"), service.HolderRepoEnv: w.repo,
		"HOLDER_TEST_MARK": mark1, service.HolderDirEnv: work1})
	cl := w.start(t, work1)
	first, err := cl.Call(holder.Start{Role: "coder"})
	if err != nil || first.Term == nil {
		t.Fatalf("start: %v\n%s", err, w.out.String())
	}
	roles := realPath(t, filepath.Join(w.repo, "roles.toml"))
	if got, want := w.mark(t, mark1), "old coder demo in "+realPath(t, work1)+" roles="+roles; got != want {
		t.Fatalf("first: %q, want %q", got, want)
	}

	manifest2 := filepath.Join(w.tmp, "other", "roles.toml")
	b, err := os.ReadFile(filepath.Join(w.repo, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manifest2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest2, b, 0o644); err != nil {
		t.Fatal(err)
	}
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin:" + w.client(t, "new"), service.HolderRepoEnv: w.repo,
		service.HolderManifestEnv: manifest2, "HOLDER_TEST_MARK": mark2, service.HolderDirEnv: work2})
	if _, err := cl.Call(holder.Start{Role: "coder"}); err != nil {
		t.Fatalf("second start: %v\n%s", err, w.out.String())
	}
	if got, want := w.mark(t, mark2), "new coder demo in "+realPath(t, work2)+" roles="+realPath(t, manifest2); got != want {
		t.Fatalf("second: %q, want %q", got, want)
	}
	r, err := cl.Call(holder.List{})
	if err != nil {
		t.Fatal(err)
	}
	alive := 0
	for _, ti := range r.Terms {
		if ti.Alive {
			alive++
		}
		if ti.ID == first.Term.ID && !ti.Alive {
			t.Fatalf("the first terminal died: %+v", r.Terms)
		}
	}
	if alive != 2 {
		t.Fatalf("terms %+v", r.Terms)
	}

	// An env file that cannot be read refuses the Start: never the stale context.
	if err := os.Chmod(w.envFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Call(holder.Start{Role: "coder"}); err == nil {
		t.Fatal("a Start with an unreadable env file was served")
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
