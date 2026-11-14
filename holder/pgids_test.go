package holder

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if err := os.WriteFile(path, []byte("100 5000\n200 6000\n300 7000\nnot a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	starts := map[int]uint64{100: 5000, 200: 6001} // 200 reused; 300 gone
	procStart := func(pid int) (uint64, error) {
		if s, ok := starts[pid]; ok {
			return s, nil
		}
		return 0, os.ErrNotExist
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
	if err != nil || n != 1 || !slices.Equal(killed, []int{-100}) {
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
