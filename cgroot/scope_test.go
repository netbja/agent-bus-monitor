package cgroot

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// resource-limits §12.5, step 4b (spec docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md):
// the scope argv sets the limits before the command runs; agent-enter moves
// its pid into the scope's client leaf, enables the controllers, and execs
// the client under that same pid.

// TestMain is also the agent-enter of the pid-contract test: with
// CGROOT_TEST_ENTER set, this binary enters the fake tree and execs argv.
func TestMain(m *testing.M) {
	if mount := os.Getenv("CGROOT_TEST_ENTER"); mount != "" {
		self := os.Getenv("CGROOT_TEST_SELF")
		fs := FS{Mount: mount, GOOS: "linux", ProcSelf: func() ([]byte, error) { return []byte("0::" + self + "\n"), nil }}
		argv := os.Args[1:]
		if i := slices.Index(argv, "--"); i >= 0 {
			argv = argv[i+1:]
		}
		err := EnterAndExec(fs, argv, os.Environ(), os.Stderr)
		os.Stderr.WriteString("exec failed: " + err.Error() + "\n")
		os.Exit(99)
	}
	os.Exit(m.Run())
}

func TestScopeUnitAndArgv(t *testing.T) {
	if got := ScopeUnit("demo", "v0.35.0.abc123+dirty", "coder", "t2"); got != "agentbus-demo-v0.35.0.abc123_dirty-coder-t2.scope" {
		t.Errorf("unit %q", got)
	}
	if ScopeKey("v0.35.0.abc-123") != "v0.35.0.abc_123" {
		t.Error("a dash in the key would split the name")
	}
	got := ScopeArgv(ScopeOpts{SystemdRun: "/usr/bin/systemd-run", Unit: "agentbus-demo-k-coder-t2.scope", MemoryMax: "8G", SwapMax: "4G",
		Enter: []string{"/bin/agentbus", "agent-enter"}, Argv: []string{"claude", "--resume", "x"}})
	want := []string{"/usr/bin/systemd-run", "--user", "--scope", "--quiet", "--slice=agentbus.slice", "--unit=agentbus-demo-k-coder-t2.scope",
		"-p", "Delegate=yes", "-p", "MemoryMax=8G", "-p", "MemorySwapMax=4G", "-p", "OOMPolicy=continue",
		"--", "/bin/agentbus", "agent-enter", "--", "claude", "--resume", "x"}
	if !slices.Equal(got, want) {
		t.Errorf("argv\n%q\nwant\n%q", got, want)
	}
	// A limit left empty is not set (the scope then has none of its own).
	if a := ScopeArgv(ScopeOpts{SystemdRun: "systemd-run", Unit: "u.scope", Enter: []string{"agentbus", "agent-enter"}, Argv: []string{"c"}}); slices.Contains(a, "MemoryMax=") || strings.Contains(strings.Join(a, " "), "MemoryMax") {
		t.Errorf("an empty limit set: %q", a)
	}
}

func TestEnter(t *testing.T) {
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	d := t.TempDir()
	tree(t, d, scope, delegated(true))
	if err := fsAt(d, scope).Enter(4242); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, scope, "client", "cgroup.procs")); string(b) != "4242" {
		t.Errorf("client/cgroup.procs = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(d, scope, "cgroup.subtree_control")); string(b) != "+memory +pids +cpu" {
		t.Errorf("subtree_control = %q", b)
	}
	// Not in a scope (the fallback path ran the client directly): refused.
	if err := fsAt(d, userSvc+"/app.slice/x.scope").Enter(1); err == nil {
		t.Error("entered outside an agentbus scope")
	}
}

// The pid contract (§12.5, r3): systemd-run execs agent-enter, which execs
// the client, all as the pid the holder started: /proc/P/comm is the
// client's, and Wait returns the client's exit.
func TestScopePidContract(t *testing.T) {
	if _, err := os.Stat("/proc/self/comm"); err != nil {
		t.Skip("no /proc on this host")
	}
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	d := t.TempDir()
	tree(t, d, scope, delegated(true))
	fake := filepath.Join(t.TempDir(), "systemd-run")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(client ...string) *exec.Cmd {
		argv := ScopeArgv(ScopeOpts{SystemdRun: fake, Unit: "agentbus-demo-k1-coder-t2.scope", MemoryMax: "1G", Enter: []string{self, "agent-enter"}, Argv: client})
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = append(os.Environ(), "CGROOT_TEST_ENTER="+d, "CGROOT_TEST_SELF="+scope)
		var errb bytes.Buffer
		c.Stderr = &errb
		return c
	}
	c := run("sleep", "2")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	deadline := time.Now().Add(3 * time.Second)
	comm := ""
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile("/proc/" + itoa(pid) + "/comm")
		if comm = strings.TrimSpace(string(b)); comm == "sleep" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if comm != "sleep" {
		t.Fatalf("/proc/%d/comm = %q, want the client's", pid, comm)
	}
	if b, _ := os.ReadFile(filepath.Join(d, scope, "client", "cgroup.procs")); string(b) != itoa(pid) {
		t.Errorf("the pid moved into client: %q, want %d", b, pid)
	}
	c.Process.Kill()
	c.Wait()
	c = run("sh", "-c", "exit 7")
	if err := c.Run(); c.ProcessState == nil || c.ProcessState.ExitCode() != 7 {
		t.Fatalf("Wait: %v, exit %v, want the client's 7", err, c.ProcessState)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
