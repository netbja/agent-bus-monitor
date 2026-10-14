package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRun records manager calls; answers maps a call prefix to its output,
// fails a prefix to an error.
type fakeRun struct {
	calls   []string
	answers map[string]string
	fails   map[string]bool
}

func (f *fakeRun) run(args ...string) ([]byte, error) {
	c := strings.Join(args, " ")
	f.calls = append(f.calls, c)
	for p := range f.fails {
		if strings.HasPrefix(c, p) {
			return []byte("failed"), errors.New("exit status 1")
		}
	}
	for p, out := range f.answers {
		if strings.HasPrefix(c, p) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func (f *fakeRun) saw(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func testSpec(t *testing.T, kind string) (Spec, Paths) {
	t.Helper()
	root := t.TempDir()
	p := Paths{Home: root, ConfigHome: filepath.Join(root, "cfg"), StateHome: filepath.Join(root, "state"), DataHome: filepath.Join(root, "data")}
	s := Spec{Project: "my-app", Kind: kind, Exe: "/opt/agent bus/agentbus", WorkDir: "/src/my app", Paths: p, EnvFile: "/cfg dir/agentbus/services/my-app.env"}
	if kind == Missions {
		s.Args = []string{"--repo", "/src/my app"}
	}
	return s, p
}

func TestSystemdUnitQuotesAndHoldsNoSecret(t *testing.T) {
	s, _ := testSpec(t, Missions)
	s.Args = append(s.Args, "--escalate-to", "50%$x")
	u := systemdUnit(s)
	for _, want := range []string{
		`ExecStart="/opt/agent bus/agentbus" "--project" "my-app" "service" "run" "missions" "--env" "/cfg dir/agentbus/services/my-app.env" "--stderr" "--repo" "/src/my app" "--escalate-to" "50%%$$x"`,
		"WorkingDirectory=/src/my app",
		"Restart=on-failure", "RestartSec=5s", "StartLimitBurst=5", "StartLimitIntervalSec=600",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit lacks %q:\n%s", want, u)
		}
	}
	for _, never := range []string{"[Install]", "WantedBy", "REDIS", "Environment"} {
		if strings.Contains(u, never) {
			t.Fatalf("unit holds %q:\n%s", never, u)
		}
	}
}

func TestSystemdInstallStartStopRemove(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{answers: map[string]string{
		"--user show agentbus-my-app-dispatch.service": "LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=4242\nResult=success\n",
	}}
	m := Systemd{Run: f.run, Paths: p}
	changed, err := m.Install(s)
	if err != nil || !changed {
		t.Fatalf("install = %v, %v", changed, err)
	}
	unit := filepath.Join(p.ConfigHome, "systemd", "user", "agentbus-my-app-dispatch.service")
	if _, err := os.Stat(unit); err != nil {
		t.Fatal(err)
	}
	if !f.saw("--user daemon-reload") {
		t.Fatalf("no daemon-reload: %v", f.calls)
	}
	f.calls = nil
	if changed, _ := m.Install(s); changed || f.saw("--user daemon-reload") {
		t.Fatalf("an unchanged unit was rewritten: %v", f.calls)
	}
	if err := m.Start(s.Project, s.Kind); err != nil {
		t.Fatal(err)
	}
	if !f.saw("--user reset-failed agentbus-my-app-dispatch.service") || !f.saw("--user start agentbus-my-app-dispatch.service") {
		t.Fatalf("start calls = %v", f.calls)
	}
	st, err := m.State(s.Project, s.Kind)
	if err != nil || !st.Installed || !st.Active || st.PID != 4242 || st.Status != "active (running)" {
		t.Fatalf("state = %+v, %v", st, err)
	}
	list, err := m.List()
	if err != nil || len(list) != 1 || list[0].Project != "my-app" || list[0].Kind != Dispatch {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := m.Stop(s.Project, s.Kind); err != nil || !f.saw("--user stop agentbus-my-app-dispatch.service") {
		t.Fatalf("stop = %v %v", err, f.calls)
	}
	if err := m.Remove(s.Project, s.Kind); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatal("remove left the unit")
	}
}

func TestSystemdRemoveKeepsTheUnitWhenTheStopFails(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	m.Install(s)
	f.fails = map[string]bool{"--user stop": true}
	if err := m.Remove(s.Project, s.Kind); err == nil {
		t.Fatal("remove succeeded although the service did not stop")
	}
	if _, err := os.Stat(filepath.Join(p.ConfigHome, "systemd", "user", UnitName(s.Project, s.Kind))); err != nil {
		t.Fatal("the unit of a service still running was deleted")
	}
}

func TestSystemdStateOfAnAbsentUnit(t *testing.T) {
	_, p := testSpec(t, Dispatch)
	f := &fakeRun{answers: map[string]string{"--user show": "LoadState=not-found\nActiveState=inactive\nSubState=dead\nMainPID=0\n"}}
	st, err := Systemd{Run: f.run, Paths: p}.State("my-app", Dispatch)
	if err != nil || st.Installed || st.Active {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestLaunchdPlistLivesOutsideLaunchAgentsAndRestartsOnFailureOnly(t *testing.T) {
	s, p := testSpec(t, Missions)
	x := launchdPlist(s)
	for _, want := range []string{
		"<key>Label</key><string>dev.agentbus.my-app.missions</string>",
		"<string>/opt/agent bus/agentbus</string>", "<string>--repo</string>", "<string>/src/my app</string>",
		"<string>--env</string><string>/cfg dir/agentbus/services/my-app.env</string>",
		"<key>SuccessfulExit</key><false/>", "<key>ThrottleInterval</key><integer>10</integer>",
		"<key>WorkingDirectory</key><string>/src/my app</string>",
		"<key>StandardOutPath</key><string>/dev/null</string>",
		"<key>StandardErrorPath</key><string>" + p.StateDir("my-app") + "/missions.crash.log</string>",
	} {
		if !strings.Contains(x, want) {
			t.Fatalf("plist lacks %q:\n%s", want, x)
		}
	}
	if strings.Contains(x, "EnvironmentVariables") || strings.Contains(x, "--stderr") {
		t.Fatalf("plist carries env or logs to stderr:\n%s", x)
	}
	f := &fakeRun{fails: map[string]bool{"print gui/": true}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(p.DataHome, "agentbus", "launchd", "dev.agentbus.my-app.missions.plist")
	if _, err := os.Stat(plist); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plist, "Library/LaunchAgents") {
		t.Fatal("a plist in LaunchAgents loads at login")
	}
	if err := m.Start(s.Project, s.Kind); err != nil {
		t.Fatal(err)
	}
	if !f.saw("bootstrap gui/501 " + plist) {
		t.Fatalf("not loaded: %v", f.calls)
	}
	f.fails = nil
	f.answers = map[string]string{"print gui/501/dev.agentbus.my-app.missions": "\tstate = running\n\tpid = 77\n"}
	f.calls = nil
	m.Start(s.Project, s.Kind)
	if !f.saw("kickstart gui/501/dev.agentbus.my-app.missions") || f.saw("bootstrap") {
		t.Fatalf("a loaded job was not kickstarted: %v", f.calls)
	}
	st, err := m.State(s.Project, s.Kind)
	if err != nil || !st.Active || st.PID != 77 {
		t.Fatalf("state = %+v, %v", st, err)
	}
	if err := m.Stop(s.Project, s.Kind); err != nil || !f.saw("bootout gui/501/dev.agentbus.my-app.missions") {
		t.Fatalf("stop = %v %v", err, f.calls)
	}
}

func TestDetect(t *testing.T) {
	ok := &fakeRun{}
	if m, why := Detect("linux", ok.run, func(string) string { return "" }); m == nil || m.Name() != "systemd" {
		t.Fatalf("linux with a user manager = %v (%s)", m, why)
	}
	down := &fakeRun{fails: map[string]bool{"--user": true}}
	if m, why := Detect("linux", down.run, func(string) string { return "" }); m != nil || !strings.Contains(why, "systemd --user") {
		t.Fatalf("linux without a user manager = %v (%s)", m, why)
	}
	if m, _ := Detect("darwin", ok.run, func(string) string { return "" }); m == nil || m.Name() != "launchd" {
		t.Fatalf("darwin = %v", m)
	}
	if m, why := Detect("linux", ok.run, func(k string) string { return map[string]string{"AGENTBUS_SERVICE_MANAGER": "none"}[k] }); m != nil || why == "" {
		t.Fatalf("forced none = %v (%s)", m, why)
	}
	if m, _ := Detect("freebsd", ok.run, func(string) string { return "" }); m != nil {
		t.Fatal("an unknown system got a manager")
	}
}
