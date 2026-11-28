package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
	"time"
)

// A developer build has no updater (quiet-update-auto §4.1): it says so and
// exits 0, final for the service manager.
func TestUpdaterRunRefusesADeveloperBuild(t *testing.T) {
	var log bytes.Buffer
	if code := runUpdater(context.Background(), updaterDeps{Repo: "", Bin: "/x/agentbus"}, &log); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(log.String(), "a developer build") {
		t.Fatalf("log %q", log.String())
	}
}

// The pass is the installed agentbus as a child, update --auto with --check
// for a check pass, its output in the updater's log.
func TestUpdaterPassRunsTheInstalledBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agentbus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"args: $*\"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if code := updaterPass(context.Background(), bin, true, &log); code != 3 {
		t.Fatalf("exit %d, log %q", code, log.String())
	}
	if !strings.Contains(log.String(), "args: update --auto --check") {
		t.Fatalf("log %q", log.String())
	}
	log.Reset()
	updaterPass(context.Background(), bin, false, &log)
	if !strings.Contains(log.String(), "args: update --auto\n") {
		t.Fatalf("log %q", log.String())
	}
}

// Stopping the loop never kills a pass in flight (§4.1): the loop returns,
// the child runs on in its own process group.
func TestUpdaterPassOutlivesTheLoop(t *testing.T) {
	dir := t.TempDir()
	bin, mark := filepath.Join(dir, "agentbus"), filepath.Join(dir, "done")
	script := "#!/bin/sh\nsleep 1\ntouch " + mark + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	if code := updaterPass(ctx, bin, false, &bytes.Buffer{}); code != -1 {
		t.Fatalf("a pass left running: exit %d, want -1", code)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatal("the loop waited for the pass")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mark); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pass was killed with the loop")
}

// The pass runs in its own process group (foureyes: Setpgid had no test).
func TestUpdaterCmdOwnProcessGroup(t *testing.T) {
	cmd := updaterCmd("/x/agentbus", true, &bytes.Buffer{})
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr %+v", cmd.SysProcAttr)
	}
	if strings.Join(cmd.Args, " ") != "/x/agentbus update --auto --check" {
		t.Fatalf("args %q", cmd.Args)
	}
}

// A missing binary is a failed pass, said in the log, never a panic.
func TestUpdaterPassMissingBinary(t *testing.T) {
	var log bytes.Buffer
	if code := updaterPass(context.Background(), filepath.Join(t.TempDir(), "none"), false, &log); code == 0 {
		t.Fatalf("exit 0, log %q", log.String())
	}
}

// service run updater needs no project: the built binary reaches the
// updater (a developer build says so), not the project services' check.
func TestServiceRunUpdaterNeedsNoProject(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(bin, "service", "run", "updater")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "REDIS_HOST=127.0.0.1", "REDIS_PORT=1"}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "updater: a developer build") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

// The unit's --env file goes into the loop's environment, so each pass
// inherits it; a bad form or an unreadable file is refused.
func TestLoadUpdaterEnv(t *testing.T) {
	f := filepath.Join(t.TempDir(), "updater.env")
	if _, err := service.WriteEnvFile(f, map[string]string{"PATH": "/opt/go/bin:/usr/bin", "REDIS_URL": "redis://x"}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	set := func(k, v string) error { got[k] = v; return nil }
	if err := loadUpdaterEnv([]string{"--env", f}, set); err != nil || got["PATH"] != "/opt/go/bin:/usr/bin" || got["REDIS_URL"] != "redis://x" {
		t.Fatalf("%v %v", err, got)
	}
	if err := loadUpdaterEnv(nil, set); err != nil {
		t.Fatalf("no file: %v", err)
	}
	for _, args := range [][]string{{"--env"}, {"--nope", "x"}, {"--env", filepath.Join(t.TempDir(), "none")}} {
		if err := loadUpdaterEnv(args, set); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}
