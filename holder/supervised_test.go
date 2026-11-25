//go:build linux

package holder

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
	"golang.org/x/sys/unix"
)

// supervisedFixture is a Supervised over temp directories whose Spawn starts
// a real stand-in supervisor (sleep, in its own session) and counts the
// spawns: what is alive is judged on real /proc entries.
type supervisedFixture struct {
	m      Supervised
	mu     sync.Mutex
	spawns []string // the argv of each spawn, joined
	pids   []int
	env    string
	dir    string
}

func newSupervised(t *testing.T) *supervisedFixture {
	t.Helper()
	xdg := t.TempDir()
	if err := os.Chmod(xdg, 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := service.Paths{Home: home, ConfigHome: filepath.Join(home, "c"), StateHome: filepath.Join(home, "s"), DataHome: filepath.Join(home, "d")}
	f := &supervisedFixture{dir: t.TempDir(), env: filepath.Join(home, "p.env")}
	if err := os.WriteFile(f.env, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.m = Supervised{
		Paths:  paths,
		Getenv: envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}),
		Spawn: func(argv []string, dir, log string) (int, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.spawns = append(f.spawns, strings.Join(argv, " "))
			c := exec.Command("sleep", "60")
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return 0, err
			}
			done := make(chan struct{}) // one Wait, in the goroutine (foureyes: -race)
			go func() { _ = c.Wait(); close(done) }()
			t.Cleanup(func() { _ = c.Process.Kill(); <-done })
			f.pids = append(f.pids, c.Process.Pid)
			return c.Process.Pid, nil
		},
		StopWait: 3 * time.Second,
	}
	return f
}

// install puts raw's binary copy in place and installs its holder.
func (f *supervisedFixture) install(t *testing.T, project, raw string) string {
	t.Helper()
	bin := service.HolderBin(f.m.Paths, service.BuildKey(raw))
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.InstallHolder(service.HolderSpec{Project: project, Raw: raw, EnvFile: f.env, WorkDir: f.dir, Paths: f.m.Paths}); err != nil {
		t.Fatal(err)
	}
	return service.BuildKey(raw)
}

func active(t *testing.T, m Supervised, project, key string) bool {
	t.Helper()
	ok, err := m.HolderActive(project, key)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// Spec 4.4b: `up` twice starts one supervisor — the second finds the first
// alive (its pid and start time) and starts nothing.
func TestSupervisedStartTwiceStartsOne(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	if active(t, f.m, "demo", key) {
		t.Fatal("installed, never started: active")
	}
	for i := 0; i < 2; i++ {
		if err := f.m.StartHolder("demo", key); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.spawns) != 1 || !active(t, f.m, "demo", key) {
		t.Fatalf("spawns %v, active %v", f.spawns, active(t, f.m, "demo", key))
	}
	want := service.HolderBin(f.m.Paths, key) + " --project demo service supervise holder demo " + key
	if f.spawns[0] != want {
		t.Fatalf("spawned %q, want %q", f.spawns[0], want)
	}
}

// Spec 4.4b: an upgrade starts the new build's supervisor beside the old one
// and leaves the old one running.
func TestSupervisedUpgradeLeavesTheOldBuild(t *testing.T) {
	f := newSupervised(t)
	a := f.install(t, "demo", "v1.0.0")
	if err := f.m.StartHolder("demo", a); err != nil {
		t.Fatal(err)
	}
	b := f.install(t, "demo", "v1.1.0")
	if err := f.m.StartHolder("demo", b); err != nil {
		t.Fatal(err)
	}
	if len(f.spawns) != 2 || !active(t, f.m, "demo", a) || !active(t, f.m, "demo", b) {
		t.Fatalf("spawns %d, old active %v, new active %v", len(f.spawns), active(t, f.m, "demo", a), active(t, f.m, "demo", b))
	}
	units, err := f.m.HolderUnits("demo")
	if err != nil || len(units) != 2 {
		t.Fatalf("units %v %v", units, err)
	}
}

// A recorded pid now held by another process (another start time) is not
// the supervisor: inactive, and a start spawns a new one.
func TestSupervisedReusedPidIsNotAlive(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	path, err := f.m.recordPath("demo", key)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readSupervisedRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := readProcStat(os.Getpid())
	if !ok {
		t.Fatal("no /proc entry for the test")
	}
	r.PID, r.Start = os.Getpid(), st.start+1 // a live pid, another life
	if err := writeSupervisedRecord(path, r); err != nil {
		t.Fatal(err)
	}
	if active(t, f.m, "demo", key) {
		t.Fatal("a reused pid reads as the supervisor")
	}
	if err := f.m.StartHolder("demo", key); err != nil || len(f.spawns) != 1 {
		t.Fatalf("start: %v, spawns %d", err, len(f.spawns))
	}
	if err := f.m.StopHolder("demo", key); err != nil {
		t.Fatal(err)
	}
}

// Stop signals the supervisor and waits for it; Remove refuses a running
// one and deletes a stopped one's record.
func TestSupervisedStopAndRemove(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	if err := f.m.StartHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	if err := f.m.RemoveHolder("demo", key); err == nil {
		t.Fatal("a running supervisor's record removed")
	}
	if err := f.m.StopHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	if active(t, f.m, "demo", key) {
		t.Fatal("stopped, still active")
	}
	if err := f.m.RemoveHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	if units, err := f.m.HolderUnits("demo"); err != nil || len(units) != 0 {
		t.Fatalf("units after remove: %v %v", units, err)
	}
	if err := f.m.RemoveHolder("demo", key); err != nil {
		t.Fatalf("a missing record: %v", err)
	}
	if err := f.m.StartHolder("demo", key); err == nil {
		t.Fatal("started a build that is not installed")
	}
}

// A gc (service.GCHolders, unchanged) removes only an old build whose
// supervisor is gone: never the current one, never a running one.
func TestSupervisedGCRemovesOnlyADeadOldBuild(t *testing.T) {
	f := newSupervised(t)
	a := f.install(t, "demo", "v1.0.0")
	b := f.install(t, "demo", "v1.1.0")
	c := f.install(t, "demo", "v1.2.0")
	for _, k := range []string{b, c} {
		if err := f.m.StartHolder("demo", k); err != nil {
			t.Fatal(err)
		}
	}
	never := func(string) bool { return false }
	removed, err := service.GCHolders(f.m, f.m.Paths, "demo", c, never)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != a {
		t.Fatalf("removed %v, want only %s", removed, a)
	}
	var keys []string
	units, _ := f.m.HolderUnits("demo")
	for _, u := range units {
		keys = append(keys, u.Key)
	}
	if strings.Join(keys, ",") != strings.Join(sortedKeys(b, c), ",") {
		t.Fatalf("left %v", keys)
	}
}

// Units are found by their recorded values, never by a name prefix: project
// a does not list a-b's; "" lists every project's.
func TestSupervisedUnitsByRecordedProject(t *testing.T) {
	f := newSupervised(t)
	f.install(t, "a", "v1.0.0")
	f.install(t, "a-b", "v1.0.0")
	for project, want := range map[string]int{"a": 1, "a-b": 1, "": 2} {
		units, err := f.m.HolderUnits(project)
		if err != nil || len(units) != want {
			t.Fatalf("%q: %v %v", project, units, err)
		}
		for _, u := range units {
			if project != "" && u.Project != project || u.Raw != "v1.0.0" {
				t.Fatalf("%q: %+v", project, u)
			}
		}
	}
}

// Security S7: a key already recorded with another raw id is refused.
func TestSupervisedRefusesAnotherRawForAKey(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	path, _ := f.m.recordPath("demo", key)
	r, err := readSupervisedRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Raw = "v1.0.0+other"
	if err := writeSupervisedRecord(path, r); err != nil {
		t.Fatal(err)
	}
	_, err = f.m.InstallHolder(service.HolderSpec{Project: "demo", Raw: "v1.0.0", EnvFile: f.env, WorkDir: f.dir, Paths: f.m.Paths})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("install over another raw: %v", err)
	}
}

// The record is read only as a private regular file of ours, never through
// a symlink.
func TestSupervisedRecordRefusesASymlink(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	path, _ := f.m.recordPath("demo", key)
	elsewhere := filepath.Join(t.TempDir(), "r")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(elsewhere, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.HolderActive("demo", key); err == nil {
		t.Fatal("a symlinked record was read")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.HolderActive("demo", key); err == nil {
		t.Fatal("a record readable by others was read")
	}
}

func sortedKeys(k ...string) []string { return slices.Sorted(slices.Values(k)) }

// The real spawn: the supervisor runs detached in its own session, in the
// recorded directory, its output in the log; a stop ends it with SIGTERM.
func TestSupervisedRealSpawnAndStop(t *testing.T) {
	f := newSupervised(t)
	f.m.Spawn = nil
	key := f.install(t, "demo", "v1.0.0")
	bin := service.HolderBin(f.m.Paths, key)
	script := "#!/bin/sh\ntrap 'echo stopped; exit 0' TERM\necho \"supervise $* in $(pwd)\"\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.m.StartHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	path, _ := f.m.recordPath("demo", key)
	r, err := readSupervisedRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(r.PID, syscall.SIGKILL) })
	if sid, err := unix.Getsid(r.PID); err != nil || sid != r.PID {
		t.Fatalf("session %d %v, want its own (%d)", sid, err, r.PID)
	}
	if !active(t, f.m, "demo", key) {
		t.Fatal("not active after start")
	}
	log := f.m.Paths.SuperviseLog("demo", service.Holder)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) { // its trap is set
		if b, _ := os.ReadFile(log); strings.HasSuffix(string(b), "\n") || time.Now().After(deadline) {
			break
		}
	}
	if err := f.m.StopHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	want := "supervise --project demo service supervise holder demo " + key + " in " + f.dir + "\nstopped\n"
	if string(b) != want {
		t.Fatalf("log %q, want %q", b, want)
	}
	if st, err := os.Stat(log); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v %v", st.Mode(), err)
	}
}

// The lock file is never unlinked (reviewer-codex): a start queued on its
// inode during a removal must share it with every later install and start,
// or two of them could each find no supervisor and both spawn one.
func TestSupervisedRemoveKeepsTheLockInode(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	path, _ := f.m.recordPath("demo", key)
	lock := strings.TrimSuffix(path, ".pid") + ".lock"
	before, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.RemoveHolder("demo", key); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lock)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the lock's inode changed across a removal: %v", err)
	}
}

// Removals, installs and starts of one build racing (a gc beside an up):
// never more than one supervisor alive.
func TestSupervisedRacingRemoveInstallStartKeepOne(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	spec := service.HolderSpec{Project: "demo", Raw: "v1.0.0", EnvFile: f.env, WorkDir: f.dir, Paths: f.m.Paths}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				_ = f.m.RemoveHolder("demo", key)
				_, _ = f.m.InstallHolder(spec)
				_ = f.m.StartHolder("demo", key)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	alive := 0
	for _, pid := range f.pids {
		if _, err := procStartOf(pid); err == nil {
			alive++
		}
	}
	if alive > 1 {
		t.Fatalf("%d supervisors alive for one build (%d spawned)", alive, len(f.pids))
	}
}

// A record whose recorded project disagrees with the directory it sits in
// is not that project's unit (foureyes: a surviving mutant).
func TestSupervisedUnitsSkipAMisplacedRecord(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "a", "v1.0.0")
	src, _ := f.m.recordPath("a", key)
	r, err := readSupervisedRecord(src)
	if err != nil {
		t.Fatal(err)
	}
	dst, _ := f.m.recordPath("b", key) // project b's place, a's record
	if err := writeSupervisedRecord(dst, r); err != nil {
		t.Fatal(err)
	}
	units, err := f.m.HolderUnits("b")
	if err != nil || len(units) != 0 {
		t.Fatalf("project b lists %v (%v)", units, err)
	}
}

// A value holding a line break would forge a field: refused, nothing written
// (foureyes: a surviving mutant).
func TestSupervisedRecordRefusesALineBreak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "holder-x.pid")
	r := supervisedRecord{Project: "demo", Key: "k", Raw: "v1", Env: "/e", WorkDir: "/w\npid 1", Bin: "/b"}
	if err := writeSupervisedRecord(path, r); err == nil {
		t.Fatal("a line break in a value was written")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused record left a file: %v", err)
	}
}

// A zombie is not a live supervisor (foureyes: a surviving mutant).
func TestProcStartOfAZombieIsGone(t *testing.T) {
	c := exec.Command("true")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	defer c.Wait()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		st, ok := readProcStat(pid)
		if !ok {
			t.Skip("reaped before it could be seen as a zombie (the package's in-process reaper)")
		}
		if st.state == 'Z' {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never a zombie: %c", st.state)
		}
	}
	if _, err := procStartOf(pid); err != errNoProcess {
		t.Fatalf("a zombie read as %v, want errNoProcess", err)
	}
}

// HolderUnitsStrict fails on a record it cannot read or that disagrees with
// its place; HolderUnits leaves it out (reviewer-codex on quiet-update 4a).
func TestSupervisedHolderUnitsStrict(t *testing.T) {
	f := newSupervised(t)
	key := f.install(t, "demo", "v1.0.0")
	if units, err := f.m.HolderUnitsStrict(""); err != nil || len(units) != 1 {
		t.Fatalf("a good record: %+v %v", units, err)
	}
	path, err := f.m.recordPath("demo", key)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(filepath.Dir(path), "holder-"+service.BuildKey("v2.0.0")+".pid")
	for body, why := range map[string]string{"{garbage": "cannot be read", string(mustRead(t, path)): "disagrees with its place"} { // unreadable; another key's record under this name
		if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if units, err := f.m.HolderUnitsStrict(""); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: strict %+v %v, want %q", body, units, err, why)
		}
		if units, err := f.m.HolderUnits(""); err != nil || len(units) != 1 {
			t.Errorf("%q: lenient %+v %v", body, units, err)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Strict, a directory of records that cannot be listed is an error, not "no
// holder" (filepath.Glob hides it; reviewer-codex on 4a r2), and one that
// does not exist holds none (RunAgentbusDir creates the run root). ProjectDir already
// refuses a project directory with another mode, in both listings, so the
// listing itself is tested on a directory it cannot read.
func TestSupervisedHolderUnitsStrictDirectories(t *testing.T) {
	f := newSupervised(t)
	if units, err := f.m.HolderUnitsStrict(""); err != nil || len(units) != 0 {
		t.Fatalf("nothing started yet: %+v %v", units, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "holder-k.pid"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if names, err := supervisedRecordNames(dir, true); err != nil || len(names) != 1 {
		t.Fatalf("readable: %v %v", names, err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if names, err := supervisedRecordNames(dir, true); err == nil || !strings.Contains(err.Error(), "cannot be listed") {
		t.Errorf("strict, unreadable: %v %v", names, err)
	}
	if names, err := supervisedRecordNames(dir, false); err != nil || len(names) != 0 {
		t.Errorf("lenient, unchanged (Glob): %v %v", names, err)
	}
	if names, err := supervisedRecordNames(filepath.Join(t.TempDir(), "absent"), true); err != nil || len(names) != 0 {
		t.Errorf("strict, absent: %v %v", names, err)
	}
}
