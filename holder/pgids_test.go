package holder

import (
	"errors"
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

// KillRecordedGroups reads the record (from a checked run directory, never
// through a symlink) and hands each line's group to the killer, which decides
// process by process (killGroupMembers); a line it cannot read is skipped.
func TestKillRecordedGroupsReadsTheRecord(t *testing.T) {
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
	if err := os.WriteFile(path, []byte("100 5000\n200 6000\n1 7\n300 0\nnot a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One cutoff for every group, taken once when the holder's death was seen
	// (reviewer-codex): never a later "now" per group.
	var asked []string
	killGroup := func(pgid int, from, to uint64) (int, error) {
		asked = append(asked, fmt.Sprintf("%d@%d-%d", pgid, from, to))
		return 2, nil
	}
	n, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", 9000, killGroup)
	if err != nil || n != 4 || !slices.Equal(asked, []string{"100@5000-9000", "200@6000-9000"}) {
		t.Fatalf("killed %d, asked %v, %v", n, asked, err)
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
	asked = nil
	if _, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", 9000, killGroup); err == nil || len(asked) != 0 {
		t.Fatalf("followed a symlink: %v %v", asked, err)
	}
	// No record: nothing to kill, no error.
	os.Remove(path)
	if n, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", 9000, killGroup); n != 0 || err != nil {
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

// groupLeader starts a session leader that ignores SIGHUP, starts a member
// in its group after delay, prints the member's pid, then exits after linger
// (0: stays). It returns the leader, its start time and the member's pid.
func groupLeader(t *testing.T, delay, linger string) (*exec.Cmd, uint64, int) {
	t.Helper()
	script := "trap '' HUP; sleep " + delay + "; sleep 300 & echo $!; "
	if linger == "0" {
		script += "exec sleep 300"
	} else {
		script += "sleep " + linger + "; exit 0"
	}
	leader := exec.Command("/bin/sh", "-c", script)
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := leader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-leader.Process.Pid, syscall.SIGKILL); leader.Wait() })
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
	return leader, start, member
}

func needProc(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc: no start time, no record")
	}
}

func waitGone(t *testing.T, pid int) bool {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if st := procState(pid); st == "" || st == "Z" {
			return true
		}
	}
	return false
}

// After a holder crash the leaders are usually gone (the hangup), while
// children in their groups live on: those are killed (foureyes), each one
// pinned by a pidfd, never the group number blindly (reviewer-codex).
func TestKillGroupMembersKillsTheSurvivorsOfAGoneLeader(t *testing.T) {
	needProc(t)
	leader, start, member := groupLeader(t, "0", "0.2")
	if err := leader.Wait(); err != nil { // the leader exited and is reaped
		t.Fatal(err)
	}
	if st := procState(member); st == "" || st == "Z" {
		t.Fatalf("the member is not running before the kill: %q", st)
	}
	if k, err := killGroupMembers(leader.Process.Pid, start, NowTicks()); err != nil || k != 1 {
		t.Fatalf("killed %d, %v", k, err)
	}
	if !waitGone(t, member) {
		t.Fatalf("the member of the gone leader's group survived (%s)", procState(member))
	}
}

// A process of that group number that started after the cutoff (the moment
// the holder's death was seen) is not the recorded group's: a group made
// after the old one emptied and its number was reused. It is left alone
// (reviewer-codex), as is everything when another process now holds the
// leader's pid.
func TestKillGroupMembersSparesWhatIsNotTheRecordedGroup(t *testing.T) {
	needProc(t)
	_, start, member := groupLeader(t, "0.3", "0")
	cutoff := start + 10 // 100 ms after the leader: before the member (300 ms)
	time.Sleep(500 * time.Millisecond)
	leaderPid := 0
	for _, pid := range groupPids(t, member) {
		leaderPid = pid
	}
	if k, err := killGroupMembers(leaderPid, start, cutoff); err != nil || k != 1 { // the leader only
		t.Fatalf("killed %d, %v", k, err)
	}
	if st := procState(member); st == "" || st == "Z" {
		t.Fatal("a member started after the cutoff was killed")
	}

	// The leader's pid held by a process with another start time: the group
	// is another's, nothing in it is touched.
	_, start2, member2 := groupLeader(t, "0", "0")
	pid2 := 0
	for _, pid := range groupPids(t, member2) {
		pid2 = pid
	}
	if k, err := killGroupMembers(pid2, start2-1, NowTicks()); err != nil || k != 0 {
		t.Fatalf("killed %d in another's group, %v", k, err)
	}
	if st := procState(member2); st == "" || st == "Z" {
		t.Fatal("a member of another's group was killed")
	}
}

// groupPids is the pgid of pid, as a one-element list (the leader's pid).
func groupPids(t *testing.T, pid int) []int {
	t.Helper()
	g, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatal(err)
	}
	return []int{g}
}

// A kill that could not be done keeps the record, the only trace of the
// groups to kill (reviewer-codex).
func TestKillRecordedGroupsKeepsTheRecordOnAFailure(t *testing.T) {
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
	if err := os.WriteFile(path, []byte("100 5000\n200 6000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	killGroup := func(pgid int, from, to uint64) (int, error) {
		if pgid == 200 {
			return 0, errors.New("pidfd_open: function not implemented")
		}
		return 1, nil
	}
	if n, err := KillRecordedGroups(getenv, "demo", "k1.0123456789", 9000, killGroup); err == nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the record was removed after a failed kill: %v", err)
	}
}
