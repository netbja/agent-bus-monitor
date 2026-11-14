package holder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/dispatch"
)

// readGroups reads a pgids file as "pid start" lines.
func readGroups(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", ":"))
}

// Spec 4.4b: without a cgroup, the holder records each terminal's process
// group (its leader's pid and start time) for its supervisor; the record
// follows the terminals: written at a start, rewritten when one ends.
func TestHolderRecordsItsTerminalsGroups(t *testing.T) {
	var path string
	_, sock := newTestHolderWith(t, func(c *Config) { path = filepath.Join(filepath.Dir(c.Socket), "holder-test.pgids"); c.Pgids = path })
	cl := dial(t, sock)
	a := startRole(t, cl, "echoer")
	b := startRole(t, cl, "exiter")
	want := []string{strconv.Itoa(a.PID) + ":" + strconv.FormatUint(a.PIDStart, 10), strconv.Itoa(b.PID) + ":" + strconv.FormatUint(b.PIDStart, 10)}
	got := readGroups(t, path)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || a.PIDStart == 0 {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st.Mode(), err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) { // exiter ends
		if g := readGroups(t, path); len(g) == 1 && g[0] == strconv.Itoa(a.PID)+":"+strconv.FormatUint(a.PIDStart, 10) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ended terminal stays recorded: %v", readGroups(t, path))
		}
	}
}

// The supervisor kills a recorded group only when its leader's pid AND start
// time still match (a reused pid, or a group whose leader changed, is left
// alone), from a checked run directory, never through a symlink.
func TestKillRecordedGroupsSkipsAReusedPid(t *testing.T) {
	run, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	os.Chmod(run, 0o700)
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return run
		}
		return ""
	}
	path, err := PgidsPath(getenv, "demo", "k1.0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("100 5000\n200 6000\n300 7000\n400 8000\nnot a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 100 the same leader; 200 reused by another process; 300 gone (its
	// group may outlive it); 400 unreadable (no decision: left alone).
	starts := map[int]uint64{100: 5000, 200: 6001}
	procStart := func(pid int) (uint64, error) {
		if s, ok := starts[pid]; ok {
			return s, nil
		}
		if pid == 300 {
			return 0, fmt.Errorf("pid %d: %w", pid, dispatch.ErrNoProcess)
		}
		return 0, os.ErrPermission
	}
	var killed []int
	kill := func(pid int, sig syscall.Signal) error {
		if sig != syscall.SIGKILL {
			t.Errorf("signal %v", sig)
		}
		killed = append(killed, pid)
		return nil
	}
	n, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", procStart, kill)
	if err != nil || n != 2 || !slices.Equal(killed, []int{-100, -300}) {
		t.Fatalf("killed %v (%d) %v", killed, n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the record stays: %v", err)
	}

	// A symlink in the record's place is never followed.
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("100 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	killed = nil
	if _, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", procStart, kill); err == nil || len(killed) != 0 {
		t.Fatalf("followed a symlink: %v %v", killed, err)
	}
	// No record: nothing to kill, no error.
	os.Remove(path)
	if n, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", procStart, kill); n != 0 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
}

// procState is /proc/<pid>/stat's state letter ("" when gone).
func procState(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2 : i+3]
	}
	return ""
}

// After a holder crash the leaders are usually gone (the hangup), while
// children in their groups live on: those are killed (foureyes). A pid is
// not reused while its process group has members, so the group reached is
// the recorded one. Real processes, Linux (ProcStart reads /proc; on macOS
// it has no start time, the holder records nothing and nothing is killed).
func TestKillRecordedGroupsKillsTheSurvivorsOfAGoneLeader(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc: no start time, no record")
	}
	run, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	os.Chmod(run, 0o700)
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return run
		}
		return ""
	}
	leader := exec.Command("/bin/sh", "-c", "trap '' HUP; sleep 300 & echo $!; sleep 0.3; exit 0")
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := leader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	start, err := dispatch.ProcStart(leader.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	var line [32]byte
	n, _ := out.Read(line[:])
	member, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		t.Fatalf("member pid %q: %v", line[:n], err)
	}
	t.Cleanup(func() { syscall.Kill(member, syscall.SIGKILL) })
	if err := leader.Wait(); err != nil { // the leader exited and is reaped
		t.Fatal(err)
	}
	path, err := PgidsPath(getenv, "demo", "k1.0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(leader.Process.Pid)+" "+strconv.FormatUint(start, 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := procState(member); st == "" || st == "Z" {
		t.Fatalf("the member is not running before the kill: %q", st)
	}
	if k, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", dispatch.ProcStart, syscall.Kill); err != nil || k != 1 {
		t.Fatalf("killed %d, %v", k, err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if st := procState(member); st == "" || st == "Z" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the member of the gone leader's group survived (%s)", procState(member))
		}
	}
}
