//go:build linux

package caprun

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain makes the test binary the helper agentbus re-executes
// (agentbus __capexec).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == HelperArg {
		os.Exit(Exec(os.Args[2:], os.Stderr))
	}
	os.Exit(m.Run())
}

// The holder unit, or its term-<id>/client (cap B, step 4), versus any other
// cgroup (agent-resource-limits §6: agentbus run tells a holder from its
// absence by its own cgroup path).
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		cgroup string
		want   Where
	}{
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.ab12.service\n", InHolder},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.ab12.service/term-3/client\n", InHolder},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.ab12.service/holder\n", InHolder},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-ghostty-123.scope\n", UserManager},
		{"0::/user.slice/user-1000.slice/session-2.scope\n", Outside},
		{"0::/\n", Outside},
		{"12:memory:/user.slice\n0::/user.slice/user-1000.slice/user@1000.service/app.slice/x.scope\n", UserManager},
		{"", Outside}, // no cgroup (macOS)
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/not-agentbus-holder.x.service\n", UserManager},
	} {
		if got := Classify(c.cgroup); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.cgroup, got, c.want)
		}
	}
}

func deps(t *testing.T, cgroup string) (Deps, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errw bytes.Buffer
	return Deps{Self: os.Args[0], Cgroup: func() string { return cgroup }, Stdout: &out, Stderr: &errw}, &out, &errw
}

const inHolder = "0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.ab12.service/term-1/client\n"

// The command is the preferred OOM victim: it inherits oom_score_adj +500
// (§2), and it runs in its own process group.
func TestRunCommandIsThePreferredVictim(t *testing.T) {
	d, out, errw := deps(t, inHolder)
	code := Run(context.Background(), Limits{}, []string{"sh", "-c", "cat /proc/self/oom_score_adj; ps -o pgid= -p $$; echo $$"}, d)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	f := strings.Fields(out.String())
	if len(f) != 3 || f[0] != "500" || f[1] != f[2] {
		t.Fatalf("oom_score_adj, pgid, pid = %q", out.String())
	}
}

// The command's own status passes through, with nothing on stderr.
func TestRunPassesTheExitThrough(t *testing.T) {
	d, _, errw := deps(t, inHolder)
	if code := Run(context.Background(), Limits{}, []string{"sh", "-c", "exit 3"}, d); code != 3 {
		t.Fatalf("exit %d", code)
	}
	if errw.Len() != 0 {
		t.Fatalf("stderr %q", errw)
	}
	// Under a memory cap too: a failing go test is not a memory problem, and
	// a line comes only with 137 (§6, Exit).
	d, _, errw = deps(t, inHolder)
	if code := Run(context.Background(), Limits{Mem: 1 << 30, MemText: "1G"}, []string{"sh", "-c", "exit 3"}, d); code != 3 || errw.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw)
	}
	if code := Run(context.Background(), Limits{}, []string{"no-such-command-xyz"}, d); code != 127 {
		t.Fatalf("missing command: exit %d (%s)", code, errw)
	}
}

// At the time cap the whole group is killed, a grandchild included: 137 and
// the time-cap line.
func TestRunTimeCapKillsTheGroup(t *testing.T) {
	d, _, errw := deps(t, inHolder)
	pidf := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	code := Run(context.Background(), Limits{Time: 300 * time.Millisecond},
		[]string{"sh", "-c", "sleep 30 & echo $! > " + pidf + "; wait"}, d)
	if code != 137 || !strings.Contains(errw.String(), "agentbus run: killed at time cap 300ms") {
		t.Fatalf("exit %d, stderr %q", code, errw)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	b, err := os.ReadFile(pidf)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	waitGone(t, pid)
}

// Without cap B, --mem is RLIMIT_AS on the command (§6): an allocation past it
// fails, the command exits non-zero, and the line says the cap is not proven.
func TestRunMemRlimit(t *testing.T) {
	d, out, errw := deps(t, inHolder)
	if code := Run(context.Background(), Limits{Mem: 64 << 20, MemText: "64M"}, []string{"sh", "-c", "ulimit -v"}, d); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if got := strings.TrimSpace(out.String()); got != strconv.Itoa(64<<10) {
		t.Fatalf("ulimit -v = %q, want %d", got, 64<<10)
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 to allocate with")
	}
	// The same allocation passes without the limit: the failure is the cap's.
	d, _, errw = deps(t, inHolder)
	alloc := []string{py, "-c", "b = bytearray(512 << 20)"}
	if code := Run(context.Background(), Limits{}, alloc, d); code != 0 {
		t.Fatalf("uncapped allocation: exit %d %s", code, errw)
	}
	d, _, errw = deps(t, inHolder)
	code := Run(context.Background(), Limits{Mem: 256 << 20, MemText: "256M"}, alloc, d)
	if code == 0 || strings.Contains(errw.String(), "agentbus run:") {
		t.Fatalf("exit %d, stderr %q", code, errw)
	}
	// No --mem: no limit.
	d, out, _ = deps(t, inHolder)
	Run(context.Background(), Limits{}, []string{"sh", "-c", "ulimit -v"}, d)
	if got := strings.TrimSpace(out.String()); got != parentAS(t) {
		t.Fatalf("no --mem: ulimit -v = %q, want the parent's %q", got, parentAS(t))
	}
}

// The leader's exit ends the run, never the time cap's watch over what it
// left in its group (reviewer-codex): the rest of the group is killed, the
// leader's status kept.
func TestRunLeaderExitKillsWhatItLeft(t *testing.T) {
	d, _, errw := deps(t, inHolder)
	pidf := filepath.Join(t.TempDir(), "pid")
	code := Run(context.Background(), Limits{Time: 30 * time.Second},
		[]string{"sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $! > " + pidf + "; exit 4"}, d)
	if code != 4 || errw.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw)
	}
	b, err := os.ReadFile(pidf)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	waitGone(t, pid)
}

// waitGone: pid is dead, or a zombie init has not reaped yet.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if stat, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); strings.Contains(string(stat), ") Z ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A SIGKILL the run did not send: 137 and the line that proves nothing.
func TestRunKilledNotProven(t *testing.T) {
	d, _, errw := deps(t, inHolder)
	if code := Run(context.Background(), Limits{}, []string{"sh", "-c", "kill -9 $$"}, d); code != 137 ||
		!strings.Contains(errw.String(), "agentbus run: killed by signal 9 (memory cap not proven)") {
		t.Fatalf("exit %d, stderr %q", code, errw)
	}
}

// In a shell no holder runs, under a user manager: systemd-run's scope
// carries the caps (§6, third row), never RLIMIT_AS. A fake systemd-run on
// PATH records its argv and runs the command.
func TestRunOutsideAHolderUsesSystemdRun(t *testing.T) {
	bin := t.TempDir()
	rec := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + rec + "\nwhile [ \"$1\" != -- ]; do shift; done; shift\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "systemd-run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	d, out, errw := deps(t, "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-x.scope\n")
	code := Run(context.Background(), Limits{Mem: 8 << 30, MemText: "8G", Time: 30 * time.Minute, CPU: 200}, []string{"sh", "-c", "ulimit -v; cat /proc/self/oom_score_adj"}, d)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	want := "--user\n--scope\n--quiet\n--collect\n-p\nMemoryMax=8G\n-p\nMemorySwapMax=0\n-p\nRuntimeMaxSec=1800\n-p\nCPUQuota=200%\n--\nsh\n-c\nulimit -v; cat /proc/self/oom_score_adj\n"
	if string(b) != want {
		t.Fatalf("systemd-run argv:\n%s\nwant:\n%s", b, want)
	}
	if got := strings.Fields(out.String()); len(got) != 2 || got[0] != parentAS(t) || got[1] != "500" {
		t.Fatalf("ulimit, adj = %q", out.String())
	}
	// No limit given: no property for it.
	d, _, _ = deps(t, "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-x.scope\n")
	Run(context.Background(), Limits{}, []string{"true"}, d)
	if b, _ := os.ReadFile(rec); string(b) != "--user\n--scope\n--quiet\n--collect\n--\ntrue\n" {
		t.Fatalf("argv without limits:\n%s", b)
	}
	// Under a holder: never systemd-run.
	_ = os.Remove(rec)
	d, _, _ = deps(t, inHolder)
	Run(context.Background(), Limits{Mem: 1 << 30, MemText: "1G"}, []string{"true"}, d)
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("systemd-run called inside a holder")
	}
}

// Outside any user manager and without systemd-run: RLIMIT_AS, as in a
// holder.
func TestRunOutsideWithoutUserManager(t *testing.T) {
	d, out, errw := deps(t, "0::/user.slice/user-1000.slice/session-2.scope\n")
	if code := Run(context.Background(), Limits{Mem: 64 << 20, MemText: "64M"}, []string{"sh", "-c", "ulimit -v"}, d); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if got := strings.TrimSpace(out.String()); got != strconv.Itoa(64<<10) {
		t.Fatalf("ulimit -v = %q", got)
	}
}

// parentAS is this process's RLIMIT_AS as sh's ulimit -v prints it: the
// command inherits it when agentbus run sets none.
func parentAS(t *testing.T) string {
	t.Helper()
	var r syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_AS, &r); err != nil {
		t.Fatal(err)
	}
	if r.Cur == ^uint64(0) { // RLIM_INFINITY
		return "unlimited"
	}
	return strconv.FormatUint(r.Cur/1024, 10)
}
