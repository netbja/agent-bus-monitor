//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// herdr exit 6c3 (spec 4.4b): with no manager, up installs dispatch and
// missions as detached supervisors (holder.Supervised, a stand-in sleep per
// spawn here), never as herdr tabs; a second up starts nothing; the marker
// names the supervisor; start_at_login is refused with the reason; down
// stops them.
func TestUpWithoutAManagerStartsSupervisors(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	run, err := os.MkdirTemp("", "upsv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	var mu sync.Mutex
	var spawns []string
	m := holder.Supervised{Paths: u.paths, Getenv: envMap(map[string]string{"XDG_RUNTIME_DIR": run}), StopWait: 3 * time.Second,
		Spawn: func(argv []string, dir, log string) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			spawns = append(spawns, strings.Join(argv, " "))
			c := exec.Command("sleep", "60")
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return 0, err
			}
			done := make(chan struct{})
			go func() { _ = c.Wait(); close(done) }()
			t.Cleanup(func() { _ = c.Process.Kill(); <-done })
			return c.Process.Pid, nil
		}}
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	want := []string{u.exe + " --project demo service supervise dispatch demo", u.exe + " --project demo service supervise missions demo"}
	if strings.Join(spawns, "\n") != strings.Join(want, "\n") {
		t.Fatalf("spawns %q, want %q", spawns, want)
	}
	if !strings.Contains(out.String(), "dispatch: started as a supervisor service") || strings.Contains(out.String(), "herdr tabs") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errw.String(), "start_at_login is on, but no service manager") || !strings.Contains(errw.String(), "KillUserProcesses") {
		t.Fatalf("stderr = %q", errw.String())
	}
	if len(u.marks) == 0 || u.marks[len(u.marks)-1].Manager != holder.ManagerName {
		t.Fatalf("marks %+v", u.marks)
	}
	out.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || len(spawns) != 2 {
		t.Fatalf("second up = %d, spawns %q: %s", code, spawns, errw.String())
	}
	if !strings.Contains(out.String(), "dispatch: already running (supervisor)") {
		t.Fatalf("second up stdout = %q", out.String())
	}
	d := serviceCmdDeps{Manager: m, Paths: u.paths, Bus: func(string) (*bus.Bus, error) { return nil, os.ErrNotExist }}
	out.Reset()
	if code := runDown([]string{"demo"}, d, &out, &errw); code != 0 {
		t.Fatalf("down = %d: %s", code, errw.String())
	}
	for _, k := range service.Kinds {
		if st, err := m.State("demo", k); err != nil || st.Active || !st.Installed {
			t.Fatalf("%s after down: %+v %v", k, st, err)
		}
	}
}
