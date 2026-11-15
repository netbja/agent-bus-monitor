//go:build linux

package holder

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
)

// serviceSpec is dispatch's or missions' spec as `up` writes it.
func (f *supervisedFixture) serviceSpec(project, kind string, args ...string) service.Spec {
	return service.Spec{Project: project, Kind: kind, Exe: "/opt/agentbus/bin/agentbus", WorkDir: f.dir,
		EnvFile: f.env, Paths: f.m.Paths, Args: args}
}

func state(t *testing.T, m Supervised, project, kind string) service.State {
	t.Helper()
	st, err := m.State(project, kind)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// herdr exit 6c3 (spec 4.4b): with no manager, dispatch and missions are
// supervised too. Install writes the record and says whether it changed;
// Start spawns `<exe> --project <p> service supervise <kind> <p>` once, a
// second start finds it alive.
func TestSupervisedServiceInstallStartOnce(t *testing.T) {
	f := newSupervised(t)
	var m service.Manager = f.m
	spec := f.serviceSpec("demo", service.Dispatch)
	for i, want := range []bool{true, false} {
		changed, err := m.Install(spec)
		if err != nil || changed != want {
			t.Fatalf("install %d: changed %v (want %v), %v", i, changed, want, err)
		}
	}
	if st := state(t, f.m, "demo", service.Dispatch); !st.Installed || st.Active {
		t.Fatalf("installed, never started: %+v", st)
	}
	for i := 0; i < 2; i++ {
		if err := m.Start("demo", service.Dispatch); err != nil {
			t.Fatal(err)
		}
	}
	want := "/opt/agentbus/bin/agentbus --project demo service supervise dispatch demo"
	if len(f.spawns) != 1 || f.spawns[0] != want {
		t.Fatalf("spawns %q, want one %q", f.spawns, want)
	}
	st := state(t, f.m, "demo", service.Dispatch)
	if !st.Active || st.PID != f.pids[0] || !strings.Contains(st.Status, "running") {
		t.Fatalf("started: %+v", st)
	}
	// Another configuration is a change: up then stops and starts it.
	if changed, err := m.Install(f.serviceSpec("demo", service.Dispatch, "--x")); err != nil || !changed {
		t.Fatalf("new args: changed %v, %v", changed, err)
	}
	if !state(t, f.m, "demo", service.Dispatch).Active {
		t.Fatal("a new install stopped the running supervisor")
	}
}

// What the supervisor runs is the unit's command (service run, its env
// file, its args) in the recorded working directory; never --stderr, as the
// supervisor's own output already is a file.
func TestSupervisedServiceCommand(t *testing.T) {
	f := newSupervised(t)
	if _, err := f.m.Install(f.serviceSpec("demo", service.Missions, "--repo", "/src/a b")); err != nil {
		t.Fatal(err)
	}
	argv, dir, err := f.m.ServiceCommand("demo", service.Missions)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/agentbus/bin/agentbus", "--project", "demo", "service", "run", "missions", "--env", f.env, "--repo", "/src/a b"}
	if !reflect.DeepEqual(argv, want) || dir != f.dir {
		t.Fatalf("argv %q in %q, want %q in %q", argv, dir, want, f.dir)
	}
	if _, _, err := f.m.ServiceCommand("demo", service.Dispatch); err == nil {
		t.Fatal("a kind never installed has a command")
	}
}

// Stop ends the supervisor (SIGTERM, waited for); Remove stops it too and
// deletes the record: the unit is gone from List and State.
func TestSupervisedServiceStopRemove(t *testing.T) {
	f := newSupervised(t)
	var m service.Manager = f.m
	for _, k := range []string{service.Dispatch, service.Missions} {
		if _, err := m.Install(f.serviceSpec("demo", k)); err != nil {
			t.Fatal(err)
		}
		if err := m.Start("demo", k); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Stop("demo", service.Dispatch); err != nil {
		t.Fatal(err)
	}
	if st := state(t, f.m, "demo", service.Dispatch); st.Active || !st.Installed {
		t.Fatalf("stopped: %+v", st)
	}
	if err := m.Remove("demo", service.Missions); err != nil {
		t.Fatal(err)
	}
	if st := state(t, f.m, "demo", service.Missions); st.Active || st.Installed {
		t.Fatalf("removed: %+v", st)
	}
	units, err := m.List()
	if err != nil || !reflect.DeepEqual(units, []service.Unit{{Project: "demo", Kind: service.Dispatch}}) {
		t.Fatalf("list %v %v", units, err)
	}
	if err := m.Remove("demo", service.Missions); err != nil {
		t.Fatalf("a missing unit: %v", err)
	}
	if err := m.Stop("demo", service.Missions); err != nil {
		t.Fatalf("stop of a missing unit: %v", err)
	}
}

// List holds every project's dispatch and missions records, never a
// holder's, nor a record whose values disagree with its place.
func TestSupervisedServiceList(t *testing.T) {
	f := newSupervised(t)
	f.install(t, "demo", "v1.0.0")
	for _, s := range []service.Spec{f.serviceSpec("demo", service.Missions), f.serviceSpec("alpha", service.Dispatch)} {
		if _, err := f.m.Install(s); err != nil {
			t.Fatal(err)
		}
	}
	// A record of alpha's, copied into demo's directory, is not demo's.
	src, _ := f.m.servicePath("alpha", service.Dispatch)
	dst, _ := f.m.servicePath("demo", service.Dispatch)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
	units, err := f.m.List()
	want := []service.Unit{{Project: "alpha", Kind: service.Dispatch}, {Project: "demo", Kind: service.Missions}}
	if err != nil || !reflect.DeepEqual(units, want) {
		t.Fatalf("list %v %v, want %v", units, err, want)
	}
}

// The holder is not a kind Install takes (it has its own per-build
// records), nor is a spec whose values would forge a field.
func TestSupervisedServiceInstallRefuses(t *testing.T) {
	f := newSupervised(t)
	bad := []service.Spec{
		f.serviceSpec("demo", service.Holder),
		f.serviceSpec("demo", "nope"),
		f.serviceSpec("Demo", service.Dispatch),
		f.serviceSpec("demo", service.Dispatch, "a\nexe /bin/evil"),
	}
	rel := f.serviceSpec("demo", service.Dispatch)
	rel.Exe = "agentbus"
	bad = append(bad, rel)
	for _, s := range bad {
		if _, err := f.m.Install(s); err == nil {
			t.Errorf("installed %+v", s)
		}
	}
	if units, _ := f.m.List(); len(units) != 0 {
		t.Fatalf("refused installs listed: %v", units)
	}
}

// A record that is a symlink is never read.
func TestSupervisedServiceRecordRefusesASymlink(t *testing.T) {
	f := newSupervised(t)
	if _, err := f.m.Install(f.serviceSpec("demo", service.Dispatch)); err != nil {
		t.Fatal(err)
	}
	path, _ := f.m.servicePath("demo", service.Dispatch)
	other := filepath.Join(t.TempDir(), "x")
	if err := os.Rename(path, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.State("demo", service.Dispatch); err == nil {
		t.Fatal("read through a symlink")
	}
}

// Linking holder registers Supervised as the manager where none runs, over
// the caller's environment.
func TestUnmanagedIsSupervised(t *testing.T) {
	getenv := envOf(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1"})
	m, ok := service.Unmanaged(service.Paths{Home: "/h"}, getenv).(Supervised)
	if !ok || m.Paths.Home != "/h" || m.getenv()("XDG_RUNTIME_DIR") != "/run/user/1" {
		t.Fatalf("unmanaged = %#v", m)
	}
}

// A detached supervisor starts with a unit's environment, not the shell's
// that ran up (herdr exit 6c3): the basics (PATH, HOME, the XDG directories
// the paths are read from) are kept; an agent's or a pane's variables are not.
func TestSpawnDetachedKeepsOnlyAUnitsEnvironment(t *testing.T) {
	t.Setenv("AGENT_BUS_AGENT", "coder")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("XDG_STATE_HOME", "/st")
	dir := t.TempDir()
	out := filepath.Join(dir, "env")
	pid, err := spawnDetached([]string{"/bin/sh", "-c", "env > " + out + ".tmp && mv " + out + ".tmp " + out}, dir, filepath.Join(dir, "log"))
	if err != nil || pid <= 0 {
		t.Fatal(pid, err)
	}
	var b []byte
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, err = os.ReadFile(out); err == nil || time.Now().After(deadline) {
			break
		}
	}
	env := "\n" + string(b)
	for _, want := range []string{"\nPATH=" + os.Getenv("PATH") + "\n", "\nHOME=" + os.Getenv("HOME") + "\n", "\nXDG_STATE_HOME=/st\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in %q", want, env)
		}
	}
	for _, leak := range []string{"AGENT_BUS_AGENT=", "HERDR_PANE_ID="} {
		if strings.Contains(env, "\n"+leak) {
			t.Errorf("leaked %s", leak)
		}
	}
}
