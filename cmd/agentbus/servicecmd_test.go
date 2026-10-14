package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// fakeManager is a service manager in memory: installed specs, active set,
// and every call.
type fakeManager struct {
	specs     map[string]service.Spec
	active    map[string]bool
	calls     []string
	failStart bool
}

func newFakeManager() *fakeManager {
	return &fakeManager{specs: map[string]service.Spec{}, active: map[string]bool{}}
}

func (f *fakeManager) Name() string { return "fake" }
func (f *fakeManager) Install(s service.Spec) (bool, error) {
	f.calls = append(f.calls, "install "+s.Project+" "+s.Kind)
	key := s.Project + "/" + s.Kind
	old, had := f.specs[key]
	f.specs[key] = s
	return !had || strings.Join(old.Args, " ") != strings.Join(s.Args, " ") || old.Exe != s.Exe, nil
}
func (f *fakeManager) Start(p, k string) error {
	f.calls = append(f.calls, "start "+p+" "+k)
	if f.failStart {
		return errors.New("start refused")
	}
	f.active[p+"/"+k] = true
	return nil
}
func (f *fakeManager) Stop(p, k string) error {
	f.calls = append(f.calls, "stop "+p+" "+k)
	f.active[p+"/"+k] = false
	return nil
}
func (f *fakeManager) Remove(p, k string) error {
	f.calls = append(f.calls, "remove "+p+" "+k)
	delete(f.specs, p+"/"+k)
	delete(f.active, p+"/"+k)
	return nil
}
func (f *fakeManager) State(p, k string) (service.State, error) {
	_, inst := f.specs[p+"/"+k]
	st := service.State{Installed: inst, Status: "inactive (dead)"}
	if f.active[p+"/"+k] {
		st.Active, st.Status, st.PID = true, "active (running)", 99
	}
	return st, nil
}
func (f *fakeManager) List() ([]service.Unit, error) {
	var out []service.Unit
	for _, k := range service.Kinds {
		for key := range f.specs {
			if p, kind, _ := strings.Cut(key, "/"); kind == k {
				out = append(out, service.Unit{Project: p, Kind: kind})
			}
		}
	}
	return out, nil
}
func (f *fakeManager) saw(c string) bool {
	for _, x := range f.calls {
		if x == c {
			return true
		}
	}
	return false
}

func cmdFixture(t *testing.T) (*fakeManager, serviceCmdDeps) {
	root := t.TempDir()
	m := newFakeManager()
	return m, serviceCmdDeps{Manager: m, Paths: service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"},
		Bus: func(string) (*bus.Bus, error) { return nil, errors.New("no broker in this test") }}
}

func TestServiceCmdStartStopLogsRemove(t *testing.T) {
	m, d := cmdFixture(t)
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	m.Install(service.Spec{Project: "demo", Kind: service.Missions})
	m.Install(service.Spec{Project: "other", Kind: service.Dispatch})
	var out, errw bytes.Buffer
	budget := service.Budget{Path: d.Paths.BudgetFile("demo", service.Dispatch)}
	for i := 0; i < service.BudgetStarts; i++ {
		budget.Failed(time.Now())
	}
	if code := runServiceCmd("demo", []string{"start"}, d, &out, &errw); code != 0 {
		t.Fatalf("start = %d %s", code, errw.String())
	}
	if !m.saw("start demo dispatch") || !m.saw("start demo missions") || m.saw("start other dispatch") {
		t.Fatalf("calls = %v", m.calls)
	}
	if budget.Spent(time.Now()) {
		t.Fatal("an explicit start kept the spent budget")
	}
	out.Reset()
	runServiceCmd("demo", []string{"status", "dispatch"}, d, &out, &errw)
	if !strings.Contains(out.String(), "dispatch  active (running), pid 99") || !strings.Contains(out.String(), "broker unreachable") {
		t.Fatalf("status = %q", out.String())
	}
	log := d.Paths.LogFile("demo", service.Dispatch)
	os.MkdirAll(filepath.Dir(log), 0o700)
	os.WriteFile(log, []byte("a\nb\nc\n"), 0o600)
	out.Reset()
	if code := runServiceCmd("demo", []string{"logs", "dispatch", "-n", "2"}, d, &out, &errw); code != 0 || out.String() != "b\nc\n" {
		t.Fatalf("logs = %d %q %s", code, out.String(), errw.String())
	}
	os.MkdirAll(filepath.Dir(d.Paths.EnvFile("demo")), 0o700)
	os.WriteFile(d.Paths.EnvFile("demo"), []byte("X=\"1\"\n"), 0o600)
	if code := runServiceCmd("demo", []string{"remove"}, d, &out, &errw); code != 0 {
		t.Fatalf("remove = %d %s", code, errw.String())
	}
	if _, err := os.Stat(d.Paths.EnvFile("demo")); !os.IsNotExist(err) {
		t.Fatal("remove left the env file")
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("remove deleted the log; it keeps data and logs")
	}
	if units, _ := m.List(); len(units) != 1 || units[0].Project != "other" {
		t.Fatalf("units left = %v", units)
	}
}

func TestServiceCmdWithoutAManagerSaysWhy(t *testing.T) {
	_, d := cmdFixture(t)
	d.Manager, d.Why = nil, "systemd --user is not available"
	var out, errw bytes.Buffer
	if code := runServiceCmd("demo", []string{"start"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), "systemd --user is not available") || !strings.Contains(errw.String(), "herdr tabs") {
		t.Fatalf("code %d, stderr %q", code, errw.String())
	}
	if code := runServiceCmd("", []string{"list"}, d, &out, &errw); code != 0 {
		t.Fatalf("list without a manager = %d", code)
	}
}

func TestServiceCmdRemoveAllNeedsNoProject(t *testing.T) {
	m, d := cmdFixture(t)
	m.Install(service.Spec{Project: "a", Kind: service.Dispatch})
	m.Install(service.Spec{Project: "b", Kind: service.Missions})
	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"remove", "--all"}, d, &out, &errw); code != 0 {
		t.Fatalf("remove --all = %d %s", code, errw.String())
	}
	if units, _ := m.List(); len(units) != 0 {
		t.Fatalf("left = %v", units)
	}
}

func TestDownStopsServicesAndExplainsAnInterruptedIntegration(t *testing.T) {
	m, d := cmdFixture(t)
	b, r := dialMain(t)
	t.Cleanup(func() { r.Del(context.Background(), bus.ServicesKey(b.Project())) })
	p := b.Project()
	m.Install(service.Spec{Project: p, Kind: service.Dispatch})
	m.Install(service.Spec{Project: p, Kind: service.Missions})
	m.Start(p, service.Dispatch)
	m.Start(p, service.Missions)
	b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: service.Kinds})
	b.PutServiceState(context.Background(), service.Missions, bus.ServiceRecord{State: "busy", Detail: "running roles-go rev 2"})
	d.Bus = func(string) (*bus.Bus, error) { return b, nil }
	var out, errw bytes.Buffer
	if code := runDown([]string{p}, d, &out, &errw); code != 0 {
		t.Fatalf("down = %d %s", code, errw.String())
	}
	if !m.saw("stop "+p+" dispatch") || !m.saw("stop "+p+" missions") || m.saw("remove "+p+" dispatch") {
		t.Fatalf("calls = %v", m.calls)
	}
	for _, want := range []string{"running roles-go rev 2", "interrupted", "redoes it at the next start", "nothing deleted", "agentbus up " + p} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("down output lacks %q:\n%s", want, out.String())
		}
	}
	if ok, _ := b.ServicesInstalled(context.Background()); !ok {
		t.Fatal("down deleted the services marker")
	}
}
