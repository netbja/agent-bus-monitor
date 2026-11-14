//go:build linux

package holder

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
)

// supervisedFixture is a Supervised over temp directories whose Spawn starts
// a real stand-in supervisor (sleep, in its own session) and counts the
// spawns: what is alive is judged on real /proc entries.
type supervisedFixture struct {
	m      Supervised
	spawns []string // the argv of each spawn, joined
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
			f.spawns = append(f.spawns, strings.Join(argv, " "))
			c := exec.Command("sleep", "60")
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return 0, err
			}
			t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
			go func() { _ = c.Wait() }()
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
