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
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

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
	if !m.Saw("start demo dispatch") || !m.Saw("start demo missions") || m.Saw("start other dispatch") {
		t.Fatalf("calls = %v", m.Calls)
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
	// A crash before the log opened (launchd's stderr file) is shown too.
	os.WriteFile(filepath.Join(filepath.Dir(log), "dispatch.crash.log"), []byte("panic: boom\n"), 0o600)
	out.Reset()
	runServiceCmd("demo", []string{"logs", "dispatch", "-n", "2"}, d, &out, &errw)
	if !strings.Contains(out.String(), "crash output") || !strings.Contains(out.String(), "panic: boom") {
		t.Fatalf("logs = %q", out.String())
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
	if code := runServiceCmd("demo", []string{"start"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), "systemd --user is not available") || !strings.Contains(errw.String(), "services.manager = none") {
		t.Fatalf("code %d, stderr %q", code, errw.String())
	}
	if code := runServiceCmd("", []string{"list"}, d, &out, &errw); code != 0 {
		t.Fatalf("list without a manager = %d", code)
	}
}

func TestServiceCmdRemoveAllNeedsNoProject(t *testing.T) {
	m, d := cmdFixture(t)
	b, r := dialMain(t)
	t.Cleanup(func() { r.Del(context.Background(), bus.ServicesKey(b.Project())) })
	b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: []string{"dispatch"}})
	d.Bus = func(p string) (*bus.Bus, error) { return bus.Open(r, p) }
	m.Install(service.Spec{Project: b.Project(), Kind: service.Dispatch})
	m.Install(service.Spec{Project: "a", Kind: service.Dispatch})
	m.Install(service.Spec{Project: "b", Kind: service.Missions})
	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"remove", "--all"}, d, &out, &errw); code != 0 {
		t.Fatalf("remove --all = %d %s", code, errw.String())
	}
	if units, _ := m.List(); len(units) != 0 {
		t.Fatalf("left = %v", units)
	}
	if ok, _ := b.ServicesInstalled(context.Background()); ok {
		t.Fatal("remove --all left the services marker")
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
	if !m.Saw("stop "+p+" dispatch") || !m.Saw("stop "+p+" missions") || m.Saw("remove "+p+" dispatch") {
		t.Fatalf("calls = %v", m.Calls)
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

// A unit list that fails after a removal never counts as "no unit left":
// the marker stays, so a remaining service is not told its project is gone.
func TestServiceRemoveKeepsTheMarkerWhenTheListFails(t *testing.T) {
	m, d := cmdFixture(t)
	b, r := dialMain(t)
	t.Cleanup(func() { r.Del(context.Background(), bus.ServicesKey(b.Project())) })
	b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}})
	d.Bus = func(p string) (*bus.Bus, error) { return bus.Open(r, p) }
	m.Install(service.Spec{Project: b.Project(), Kind: service.Dispatch})
	m.Install(service.Spec{Project: b.Project(), Kind: service.Missions})
	m.FailList = true
	var out, errw bytes.Buffer
	runServiceCmd(b.Project(), []string{"remove", "dispatch"}, d, &out, &errw)
	runServiceCmd("", []string{"remove", "--all"}, d, &out, &errw)
	if ok, _ := b.ServicesInstalled(context.Background()); !ok {
		t.Fatal("the marker was cleared on a failed unit list")
	}
}

// A service that crashed before its log existed still shows something.
func TestServiceLogsShowsACrashWithoutALog(t *testing.T) {
	m, d := cmdFixture(t)
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	crash := d.Paths.CrashFile("demo", service.Dispatch)
	os.MkdirAll(filepath.Dir(crash), 0o700)
	os.WriteFile(crash, []byte("fatal error: early\n"), 0o600)
	var out, errw bytes.Buffer
	if code := runServiceCmd("demo", []string{"logs", "dispatch"}, d, &out, &errw); code != 0 || !strings.Contains(out.String(), "fatal error: early") {
		t.Fatalf("logs = %d %q %q", code, out.String(), errw.String())
	}
}

// detachedFake is a manager whose services run as detached supervisors
// (holder.Supervised's shape): it states its limits.
type detachedFake struct{ *fakeManager }

func (detachedFake) Limits() string { return "LIMITS" }

// herdr exit 6c3 (spec 4.4b): without a manager, service status states what
// does not exist (no start at login, a logout may end the services) and
// names the supervisor's log, which service logs shows too; service login
// with the option on is refused with that reason.
func TestServiceCmdDetachedSaysItsLimits(t *testing.T) {
	fm, d := cmdFixture(t)
	d.Manager = detachedFake{fm}
	fm.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	var out, errw bytes.Buffer
	runServiceCmd("demo", []string{"status"}, d, &out, &errw)
	slog := d.Paths.SuperviseLog("demo", service.Dispatch)
	if !strings.Contains(out.String(), "LIMITS") || !strings.Contains(out.String(), "supervisor log: "+slog) {
		t.Fatalf("status = %q", out.String())
	}
	os.MkdirAll(filepath.Dir(slog), 0o700)
	os.WriteFile(slog, []byte("supervise: dispatch of project demo (pid 7)\n"), 0o600)
	out.Reset()
	if code := runServiceCmd("demo", []string{"logs", "dispatch"}, d, &out, &errw); code != 0 ||
		!strings.Contains(out.String(), "== dispatch supervisor ("+slog+")\nsupervise: dispatch of project demo (pid 7)\n") {
		t.Fatalf("logs = %d %q %s", code, out.String(), errw.String())
	}
	d.AtLogin = func() bool { return true }
	out.Reset()
	errw.Reset()
	if code := runServiceCmd("demo", []string{"login"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), "LIMITS") {
		t.Fatalf("login = %d %q", code, errw.String())
	}
}

type fakeManager = cmdtest.Manager

var newFakeManager = cmdtest.NewManager
