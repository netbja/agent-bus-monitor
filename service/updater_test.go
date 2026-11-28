package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The updater's systemd unit (quiet-update-auto §4.1): one per host, no
// project, agentbus service run updater with its env file, KillMode=process
// so stopping the loop never kills a pass; never listed as a project's
// service or a holder.
func TestSystemdUpdaterUnit(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	s := UpdaterSpec{Exe: "/bin/100%/agentbus", WorkDir: p.Home, Paths: p}
	if changed, err := m.InstallUpdater(s); err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	if UpdaterUnitName != "agentbus-updater.service" {
		t.Fatalf("name %s", UpdaterUnitName)
	}
	b, err := os.ReadFile(filepath.Join(p.ConfigHome, "systemd", "user", UpdaterUnitName))
	if err != nil {
		t.Fatal(err)
	}
	u := string(b)
	for _, want := range []string{
		`ExecStart="/bin/100%%/agentbus" "service" "run" "updater" "--env" "` + filepath.Join(p.ConfigHome, "agentbus", "updater.env") + `"`,
		"KillMode=process\n", "Restart=on-failure\n", "WorkingDirectory=" + p.Home + "\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "--project") || strings.Contains(u, "control-group") {
		t.Errorf("a host unit has no project and kills no group:\n%s", u)
	}
	if !slices.Contains(f.calls, "--user daemon-reload") {
		t.Errorf("calls %v", f.calls)
	}
	if changed, _ := m.InstallUpdater(s); changed {
		t.Error("an unchanged unit reported changed")
	}
	if units, _ := m.List(); len(units) != 0 {
		t.Errorf("listed as a project's service: %v", units)
	}
	if hs, err := m.HolderUnitsStrict(""); err != nil || len(hs) != 0 {
		t.Errorf("seen as a holder: %v %v", hs, err)
	}
	f.calls = nil
	if err := m.StartUpdater(); err != nil {
		t.Fatal(err)
	}
	if err := m.StopUpdater(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.calls, "--user start "+UpdaterUnitName) || !slices.Contains(f.calls, "--user stop "+UpdaterUnitName) {
		t.Errorf("calls %v", f.calls)
	}
	f.out = map[string]fakeOut{"--user show " + UpdaterUnitName: {text: "LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=42\n"}}
	st, err := m.UpdaterState()
	if err != nil || !st.Active || !st.Installed || st.PID != 42 {
		t.Fatalf("state %+v %v", st, err)
	}
	if err := m.RemoveUpdater(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.ConfigHome, "systemd", "user", UpdaterUnitName)); !os.IsNotExist(err) {
		t.Fatalf("unit left: %v", err)
	}
	if err := m.RemoveUpdater(); err != nil {
		t.Fatalf("a missing unit is no error: %v", err)
	}
}

// The updater's launchd job: dev.agentbus.updater, AbandonProcessGroup so an
// unload leaves a pass running, restarted on a failure only, its stderr in
// the updater's log.
func TestLaunchdUpdaterJob(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	if _, err := m.InstallUpdater(UpdaterSpec{Exe: "/bin/a&b/agentbus", WorkDir: p.Home, Paths: p}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(p.DataHome, "agentbus", "launchd", UpdaterLabel+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	pl := string(b)
	for _, want := range []string{
		"<key>Label</key><string>dev.agentbus.updater</string>",
		"<string>/bin/a&amp;b/agentbus</string><string>service</string><string>run</string><string>updater</string><string>--env</string>",
		"<key>AbandonProcessGroup</key><true/>",
		"<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>",
		"<key>StandardErrorPath</key><string>" + p.UpdaterLog() + "</string>",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("plist lacks %q:\n%s", want, pl)
		}
	}
	if units, _ := m.List(); len(units) != 0 {
		t.Errorf("listed as a project's service: %v", units)
	}
	f.calls = nil
	f.fails = map[string]bool{"print gui/501/dev.agentbus.updater": true}
	if err := m.StartUpdater(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.calls, "bootstrap gui/501 "+filepath.Join(p.DataHome, "agentbus", "launchd", UpdaterLabel+".plist")) {
		t.Errorf("calls %v", f.calls)
	}
	if st, _ := m.UpdaterState(); st.Active || !st.Installed {
		t.Errorf("not loaded: %+v", st)
	}
	if err := m.RemoveUpdater(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.DataHome, "agentbus", "launchd", UpdaterLabel+".plist")); !os.IsNotExist(err) {
		t.Fatalf("plist left: %v", err)
	}
}
