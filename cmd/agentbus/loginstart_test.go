package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// fakeLogin is a service manager with a login backend (self-repair step 9).
type fakeLogin struct {
	*fakeManager
	enabled     map[string]bool
	failEnble   bool
	failDisable bool
	enabledErr  error
}

func newFakeLogin() *fakeLogin {
	return &fakeLogin{fakeManager: newFakeManager(), enabled: map[string]bool{}}
}

func (f *fakeLogin) Enable(p string) error {
	f.Calls = append(f.Calls, "enable "+p)
	if f.failEnble {
		return errors.New("systemctl --user enable: refused")
	}
	f.enabled[p] = true
	return nil
}
func (f *fakeLogin) Disable(p string) error {
	f.Calls = append(f.Calls, "disable "+p)
	if f.failDisable {
		return errors.New("systemctl --user disable: refused")
	}
	f.enabled[p] = false
	return nil
}
func (f *fakeLogin) Enabled(p string) (bool, error) {
	if f.enabledErr != nil {
		return false, f.enabledErr
	}
	return f.enabled[p], nil
}

func (f *fakeLogin) index(c string) int {
	for i, x := range f.Calls {
		if x == c {
			return i
		}
	}
	return -1
}

// up with the option on writes the units started at login and enables them,
// after they are installed.
func TestUpEnablesLoginStartWhenTheOptionIsOn(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	m := newFakeLogin()
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	_ = u
	if !m.Specs["demo/dispatch"].AtLogin || !m.Specs["demo/missions"].AtLogin {
		t.Fatalf("specs = %+v", m.Specs)
	}
	if i := m.index("enable demo"); i < 0 || i < m.index("install demo missions") {
		t.Fatalf("calls = %v (enable after the install)", m.Calls)
	}
	if !strings.Contains(out.String(), "start at login") {
		t.Fatalf("out = %q", out.String())
	}
}

// up with the option off disables a project that was enabled, before its
// units lose their [Install] section (systemd disables by that section).
func TestUpDisablesLoginStartWhenTheOptionIsOff(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	withServices(t, &deps)
	m := newFakeLogin()
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch, AtLogin: true})
	m.enabled["demo"] = true
	m.Calls = nil
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if i := m.index("disable demo"); i < 0 || i > m.index("install demo dispatch") {
		t.Fatalf("calls = %v (disable before the install)", m.Calls)
	}
	if m.Specs["demo/dispatch"].AtLogin {
		t.Fatal("unit still written for login")
	}
	// Off and never enabled: nothing is touched.
	m.Calls = nil
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || m.index("disable demo") >= 0 || m.index("enable demo") >= 0 {
		t.Fatalf("up = %d, calls = %v", code, m.Calls)
	}
}

// With the option on and no backend (another manager, or herdr tabs), up
// says why it is refused and carries on.
func TestUpRefusesLoginStartWithoutABackend(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if !strings.Contains(errw.String(), service.NoLoginStart) || !u.m.Saw("start demo dispatch") {
		t.Fatalf("stderr = %q, calls = %v", errw.String(), u.m.Calls)
	}
	work, _, _, _, deps = upFixture(t)
	deps.Services = func() (service.Manager, string) { return nil, "systemd --user is not available" }
	deps.AtLogin = func() bool { return true }
	out.Reset()
	errw.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || !strings.Contains(errw.String(), "no service manager") {
		t.Fatalf("up without a manager = %d: %s", code, errw.String()) // herdr exit §4.4b: no tab mode any more
	}
}

// A failed enable is reported, and the services still run.
func TestUpReportsAFailedEnable(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	withServices(t, &deps)
	m := newFakeLogin()
	m.failEnble = true
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	if !strings.Contains(errw.String(), "enable: refused") || !m.Saw("start demo dispatch") {
		t.Fatalf("stderr = %q, calls = %v", errw.String(), m.Calls)
	}
}

// service login applies the machine's option to every project with services
// installed, at once, and says where each stands.
func TestServiceLoginAppliesTheOption(t *testing.T) {
	_, d := cmdFixture(t)
	m := newFakeLogin()
	d.Manager = m
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	m.Install(service.Spec{Project: "demo", Kind: service.Missions})
	m.Install(service.Spec{Project: "other", Kind: service.Dispatch})
	d.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 {
		t.Fatalf("login = %d: %s", code, errw.String())
	}
	if !m.Saw("enable demo") || !m.Saw("enable other") || strings.Count(strings.Join(m.Calls, ";"), "enable demo") != 1 {
		t.Fatalf("calls = %v", m.Calls)
	}
	for _, want := range []string{"demo: starts at login", "other: starts at login"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out = %q", out.String())
		}
	}
	d.AtLogin = func() bool { return false }
	m.enabled["other"] = false
	out.Reset()
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 {
		t.Fatalf("login off = %d: %s", code, errw.String())
	}
	if !m.Saw("disable demo") || m.Saw("disable other") {
		t.Fatalf("calls = %v", m.Calls)
	}
	for _, want := range []string{"demo: no longer starts at login", "other: does not start at login"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out = %q", out.String())
		}
	}
}

// Without a backend, the option on is refused with the reason; off, nothing
// needs doing.
func TestServiceLoginWithoutABackend(t *testing.T) {
	_, d := cmdFixture(t)
	d.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), service.NoLoginStart) {
		t.Fatalf("code %d, stderr %q", code, errw.String())
	}
	d.Manager, d.Why = nil, "systemd --user is not available"
	errw.Reset()
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), service.NoLoginStart) {
		t.Fatalf("no manager: code %d, stderr %q", code, errw.String())
	}
	d.AtLogin = func() bool { return false }
	out.Reset()
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 || !strings.Contains(out.String(), "off") {
		t.Fatalf("off, no manager: code %d, out %q", code, out.String())
	}
}

// Off, a project whose login state cannot be read is disabled anyway, before
// the install; when that disable fails too, up writes no unit and fails
// (reviewer-codex: never strip the section while the link may remain).
func TestUpOffDisablesWhenTheStateIsUnknown(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	withServices(t, &deps)
	m := newFakeLogin()
	m.enabledErr = errors.New("is-enabled: Failed to connect to bus")
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if i := m.index("disable demo"); i < 0 || i > m.index("install demo dispatch") {
		t.Fatalf("calls = %v (disable before the install)", m.Calls)
	}
	m.Calls, m.failDisable = nil, true
	errw.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
		t.Fatal("up succeeded with login start neither read nor disabled")
	}
	if m.index("install demo dispatch") >= 0 || !strings.Contains(errw.String(), "start at login") {
		t.Fatalf("calls = %v, stderr = %q", m.Calls, errw.String())
	}
}

// service login, off: an unreadable state is disabled anyway; a failure is
// said and exits 1.
func TestServiceLoginOffWithAnUnknownState(t *testing.T) {
	_, d := cmdFixture(t)
	m := newFakeLogin()
	d.Manager = m
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	m.enabledErr = errors.New("is-enabled: Failed to connect to bus")
	d.AtLogin = func() bool { return false }
	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 || !m.Saw("disable demo") {
		t.Fatalf("code %d, calls %v, stderr %q", code, m.Calls, errw.String())
	}
	m.failDisable = true
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 1 {
		t.Fatalf("a failed disable exited %d", code)
	}
}

// partlyEnabled is a real systemd backend over a fake systemctl whose unit
// of kind on is enabled and the other not (foureyes' case, and its reverse).
func partlyEnabled(t *testing.T, paths service.Paths, on string) (service.Systemd, *[]string) {
	t.Helper()
	calls := &[]string{}
	run := func(args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if len(args) == 3 && args[1] == "is-enabled" {
			if strings.Contains(args[2], "-"+on+".service") {
				return []byte("enabled\n"), nil
			}
			return []byte("disabled\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	m := service.Systemd{Run: run, Paths: paths}
	for _, k := range []string{service.Dispatch, service.Missions} {
		if _, err := m.Install(service.Spec{Project: "demo", Kind: k, Exe: "/opt/ab/agentbus", Paths: paths}); err != nil {
			t.Fatal(err)
		}
	}
	*calls = nil
	return m, calls
}

func callIndex(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// up, option off, a partly enabled project: every unit is disabled before
// any is rewritten.
func TestUpOffDisablesAPartlyEnabledProject(t *testing.T) {
	for _, on := range []string{service.Dispatch, service.Missions} {
		work, _, _, _, deps := upFixture(t)
		u := withServices(t, &deps)
		m, calls := partlyEnabled(t, u.paths, on)
		deps.Services = func() (service.Manager, string) { return m, "" }
		deps.AtLogin = func() bool { return false }
		var out, errw bytes.Buffer
		runUp([]string{"demo", work}, deps, &out, &errw)
		d := callIndex(*calls, "--user disable agentbus-demo-dispatch.service")
		dm := callIndex(*calls, "--user disable agentbus-demo-missions.service")
		if d < 0 || dm < 0 {
			t.Fatalf("%s enabled: calls = %v", on, *calls)
		}
		if r := callIndex(*calls, "--user daemon-reload"); r >= 0 && (r < d || r < dm) {
			t.Fatalf("%s enabled: a unit was rewritten before the disable: %v", on, *calls)
		}
	}
}

// service login, option off, a partly enabled project: disabled.
func TestServiceLoginOffDisablesAPartlyEnabledProject(t *testing.T) {
	for _, on := range []string{service.Dispatch, service.Missions} {
		_, d := cmdFixture(t)
		m, calls := partlyEnabled(t, d.Paths, on)
		d.Manager = m
		d.AtLogin = func() bool { return false }
		var out, errw bytes.Buffer
		if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 {
			t.Fatalf("%s enabled: login = %d: %s", on, code, errw.String())
		}
		if callIndex(*calls, "--user disable agentbus-demo-"+on+".service") < 0 || !strings.Contains(out.String(), "demo: no longer starts at login") {
			t.Fatalf("%s enabled: calls = %v, out = %q", on, *calls, out.String())
		}
	}
}
