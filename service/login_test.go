package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Start at login (self-repair step 9, spec 4.4): a systemd unit gains
// [Install] WantedBy=default.target only while the option is on, and is
// enabled; off, it is disabled and loses the section. No linger.
func TestSystemdLoginStart(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{answers: map[string]string{"--user is-enabled": "disabled\n"}}
	m := Systemd{Run: f.run, Paths: p}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(p.ConfigHome, "systemd", "user", "agentbus-my-app-dispatch.service")
	var ls LoginStart = m
	if on, err := ls.Enabled("my-app"); err != nil || on {
		t.Fatalf("enabled before any enable: %v %v", on, err)
	}
	f.calls = nil
	if err := ls.Enable("my-app"); err != nil {
		t.Fatal(err)
	}
	if b := string(mustReadFile(t, unit)); !strings.HasSuffix(b, "\n[Install]\nWantedBy=default.target\n") {
		t.Fatalf("enabled unit:\n%s", b)
	}
	if !f.saw("--user daemon-reload") || !f.saw("--user enable agentbus-my-app-dispatch.service") || f.saw("--user enable --now") {
		t.Fatalf("calls = %v (enable, never start)", f.calls)
	}
	if strings.Contains(strings.Join(f.calls, ";"), "linger") {
		t.Fatalf("linger touched: %v", f.calls)
	}
	// Enabling twice leaves one section.
	if err := ls.Enable("my-app"); err != nil || strings.Count(string(mustReadFile(t, unit)), "[Install]") != 1 {
		t.Fatalf("second enable: %v\n%s", err, mustReadFile(t, unit))
	}
	f.answers = map[string]string{"--user is-enabled agentbus-my-app-dispatch.service": "enabled\n"}
	if on, err := ls.Enabled("my-app"); err != nil || !on {
		t.Fatalf("enabled = %v %v", on, err)
	}
	// An `up` with the option on writes the same unit: no change, no restart.
	s.AtLogin = true
	if changed, err := m.Install(s); err != nil || changed {
		t.Fatalf("install with the option on changed the enabled unit: %v %v", changed, err)
	}
	f.calls = nil
	if err := ls.Disable("my-app"); err != nil {
		t.Fatal(err)
	}
	if b := string(mustReadFile(t, unit)); strings.Contains(b, "[Install]") || strings.Contains(b, "WantedBy") {
		t.Fatalf("disabled unit keeps its section:\n%s", b)
	}
	if !f.saw("--user disable agentbus-my-app-dispatch.service") || f.saw("--user stop") {
		t.Fatalf("calls = %v (disable, never stop)", f.calls)
	}
	s.AtLogin = false
	if changed, err := m.Install(s); err != nil || changed {
		t.Fatalf("install with the option off changed the disabled unit: %v %v", changed, err)
	}
}

// Every installed service of the project is enabled, and only that project's.
func TestSystemdLoginStartCoversTheProjectsServicesOnly(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	other := s
	other.Project = "other"
	missions, _ := testSpec(t, Missions)
	missions.Paths = p
	for _, sp := range []Spec{s, missions, other} {
		if _, err := m.Install(sp); err != nil {
			t.Fatal(err)
		}
	}
	f.calls = nil
	if err := m.Enable("my-app"); err != nil {
		t.Fatal(err)
	}
	if !f.saw("--user enable agentbus-my-app-dispatch.service") || !f.saw("--user enable agentbus-my-app-missions.service") || f.saw("--user enable agentbus-other") {
		t.Fatalf("calls = %v", f.calls)
	}
	// A project with nothing installed has nothing to start at login.
	if err := m.Enable("ghost"); err == nil || !strings.Contains(err.Error(), "agentbus up") {
		t.Fatalf("enable with no service: %v", err)
	}
	if on, err := m.Enabled("ghost"); err != nil || on {
		t.Fatalf("ghost enabled = %v %v", on, err)
	}
}

// A failed systemctl enable is an error, and the half-written section does
// not claim the unit is enabled.
func TestSystemdEnableFailure(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{fails: map[string]bool{"--user enable": true}}
	m := Systemd{Run: f.run, Paths: p}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable("my-app"); err == nil || !strings.Contains(err.Error(), "systemctl --user enable") {
		t.Fatalf("enable = %v", err)
	}
}

// Removing an enabled unit disables it first: no dangling wants link.
func TestSystemdRemoveDisablesAnEnabledUnit(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	s.AtLogin = true
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	f.answers = map[string]string{"--user is-enabled": "enabled\n"}
	f.calls = nil
	if err := m.Remove("my-app", Dispatch); err != nil {
		t.Fatal(err)
	}
	if !f.saw("--user disable agentbus-my-app-dispatch.service") {
		t.Fatalf("calls = %v", f.calls)
	}
	f.calls = nil
	f.answers = map[string]string{"--user is-enabled": "disabled\n"}
	s.AtLogin = false
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("my-app", Dispatch); err != nil || f.saw("--user disable") {
		t.Fatalf("a unit never enabled was disabled: %v %v", err, f.calls)
	}
}

// macOS: a copy of the job's plist in ~/Library/LaunchAgents (RunAtLoad)
// while the option is on; off, the copy is removed and the running job left
// alone.
func TestLaunchdLoginStart(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{fails: map[string]bool{"print gui/": true}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(p.Home, "Library", "LaunchAgents", "dev.agentbus.my-app.dispatch.plist")
	var ls LoginStart = m
	if on, _ := ls.Enabled("my-app"); on {
		t.Fatal("enabled before any enable")
	}
	if err := ls.Enable("my-app"); err != nil {
		t.Fatal(err)
	}
	b := string(mustReadFile(t, agent))
	if b != launchdPlist(s) || !strings.Contains(b, "<key>RunAtLoad</key><true/>") {
		t.Fatalf("LaunchAgents copy:\n%s", b)
	}
	if on, err := ls.Enabled("my-app"); err != nil || !on {
		t.Fatalf("enabled = %v %v", on, err)
	}
	// An up with the option on keeps the copy in step with the job.
	s.Args = []string{"--escalate-to", "master"}
	s.AtLogin = true
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	if string(mustReadFile(t, agent)) != launchdPlist(s) {
		t.Fatal("the LaunchAgents copy was not updated with the job")
	}
	f.calls = nil
	if err := ls.Disable("my-app"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent); !os.IsNotExist(err) {
		t.Fatalf("copy left: %v", err)
	}
	if f.saw("bootout") {
		t.Fatalf("disable stopped the job: %v", f.calls)
	}
	if _, err := os.Stat(m.path("my-app", Dispatch)); err != nil {
		t.Fatalf("disable removed the job's own plist: %v", err)
	}
	// Remove takes the copy too.
	if err := ls.Enable("my-app"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("my-app", Dispatch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent); !os.IsNotExist(err) {
		t.Fatalf("remove left the LaunchAgents copy: %v", err)
	}
}

// A LaunchAgents file agentbus did not write is never changed or removed:
// disable, remove and an up leave it; enable refuses to overwrite it.
func TestLaunchdDisableKeepsAForeignFile(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{fails: map[string]bool{"print gui/": true}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(p.Home, "Library", "LaunchAgents", "dev.agentbus.my-app.dispatch.plist")
	must(t, os.MkdirAll(filepath.Dir(agent), 0o755))
	must(t, os.WriteFile(agent, []byte("<plist>the person's own</plist>"), 0o644))
	if err := m.Disable("my-app"); err != nil {
		t.Fatalf("disable with a foreign file: %v", err)
	}
	if err := m.Remove("my-app", Dispatch); err != nil {
		t.Fatalf("remove with a foreign file: %v", err)
	}
	if _, err := m.Install(s); err != nil {
		t.Fatalf("an up with the option off and a foreign file: %v", err)
	}
	if string(mustReadFile(t, agent)) != "<plist>the person's own</plist>" {
		t.Fatal("a foreign file was changed")
	}
	if err := m.Enable("my-app"); err == nil {
		t.Fatal("enable overwrote a foreign file")
	}
}

// Elsewhere, enabling is refused with the reason.
func TestLoginStartOf(t *testing.T) {
	_, p := testSpec(t, Dispatch)
	if ls, why := LoginStartOf(Systemd{Paths: p}); ls == nil || why != "" {
		t.Fatalf("systemd: %v %q", ls, why)
	}
	if ls, why := LoginStartOf(Launchd{Paths: p}); ls == nil || why != "" {
		t.Fatalf("launchd: %v %q", ls, why)
	}
	if ls, why := LoginStartOf(nil); ls != nil || why != "no service manager to start at login" {
		t.Fatalf("none: %v %q", ls, why)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// is-enabled exits non-zero for a disabled unit: its answer is read from its
// output; anything that is not a state is an error, never "disabled"
// (reviewer-codex: a failed read must not let a wants link survive).
func TestSystemdEnabledReadsTheStateNotTheExitCode(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	answer, fail := "", false
	run := func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "is-enabled" {
			if fail {
				return []byte(answer), errors.New("exit status 1")
			}
			return []byte(answer), nil
		}
		return nil, nil
	}
	m := Systemd{Run: run, Paths: p}
	if _, err := m.Install(s); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		out  string
		fail bool
		on   bool
		err  bool
	}{
		{"enabled\n", false, true, false},
		{"disabled\n", true, false, false},
		{"static\n", false, false, false},
		{"linked\n", true, true, false}, // a login may start a linked unit
		{"Failed to connect to bus: No medium found\n", true, false, true},
		{"", true, false, true},
	} {
		answer, fail = c.out, c.fail
		on, err := m.Enabled("my-app")
		if on != c.on || (err != nil) != c.err {
			t.Errorf("is-enabled %q (fail %v): %v %v", c.out, c.fail, on, err)
		}
	}
}

// A partly enabled project (an Enable that failed half way, a unit disabled
// by hand) counts as starting at login: the option going off disables every
// unit (foureyes).
func TestSystemdPartlyEnabledCountsAsEnabled(t *testing.T) {
	s, p := testSpec(t, Dispatch)
	f := &fakeRun{fails: map[string]bool{"--user enable agentbus-my-app-missions.service": true}}
	m := Systemd{Run: f.run, Paths: p}
	missions, _ := testSpec(t, Missions)
	missions.Paths = p
	for _, sp := range []Spec{s, missions} {
		if _, err := m.Install(sp); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Enable("my-app"); err == nil {
		t.Fatal("the missions enable failure was swallowed")
	}
	f.fails = nil
	f.answers = map[string]string{
		"--user is-enabled agentbus-my-app-dispatch.service": "enabled\n",
		"--user is-enabled agentbus-my-app-missions.service": "disabled\n",
	}
	if on, err := m.Enabled("my-app"); err != nil || !on {
		t.Fatalf("partly enabled = %v %v", on, err)
	}
	f.calls = nil
	if err := m.Disable("my-app"); err != nil {
		t.Fatal(err)
	}
	if !f.saw("--user disable agentbus-my-app-dispatch.service") || !f.saw("--user disable agentbus-my-app-missions.service") {
		t.Fatalf("calls = %v", f.calls)
	}
}

// Remove disables a unit that is-enabled says starts at login even when its
// section is gone, and one whose state cannot be read; a disabled unit
// without the section is not touched (foureyes).
func TestSystemdRemoveDisablesByTheState(t *testing.T) {
	for _, c := range []struct {
		answer  string
		fail    bool
		disable bool
	}{
		{"enabled\n", false, true},
		{"linked\n", true, true},
		{"Failed to connect to bus\n", true, true},
		{"disabled\n", true, false},
		{"static\n", false, false},
	} {
		s, p := testSpec(t, Dispatch)
		var calls []string
		run := func(args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if len(args) > 1 && args[1] == "is-enabled" {
				if c.fail {
					return []byte(c.answer), errors.New("exit status 1")
				}
				return []byte(c.answer), nil
			}
			return nil, nil
		}
		m := Systemd{Run: run, Paths: p}
		if _, err := m.Install(s); err != nil { // no [Install] section
			t.Fatal(err)
		}
		if err := m.Remove("my-app", Dispatch); err != nil {
			t.Fatalf("%q: %v", c.answer, err)
		}
		if got := strings.Contains(strings.Join(calls, ";"), "--user disable"); got != c.disable {
			t.Errorf("is-enabled %q: disabled %v, want %v (%v)", c.answer, got, c.disable, calls)
		}
	}
}
