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
	f.calls = append(f.calls, "enable "+p)
	if f.failEnble {
		return errors.New("systemctl --user enable: refused")
	}
	f.enabled[p] = true
	return nil
}
func (f *fakeLogin) Disable(p string) error {
	f.calls = append(f.calls, "disable "+p)
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
	for i, x := range f.calls {
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
	if !m.specs["demo/dispatch"].AtLogin || !m.specs["demo/missions"].AtLogin {
		t.Fatalf("specs = %+v", m.specs)
	}
	if i := m.index("enable demo"); i < 0 || i < m.index("install demo missions") {
		t.Fatalf("calls = %v (enable after the install)", m.calls)
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
	m.calls = nil
	deps.Services = func() (service.Manager, string) { return m, "" }
	deps.AtLogin = func() bool { return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if i := m.index("disable demo"); i < 0 || i > m.index("install demo dispatch") {
		t.Fatalf("calls = %v (disable before the install)", m.calls)
	}
	if m.specs["demo/dispatch"].AtLogin {
		t.Fatal("unit still written for login")
	}
	// Off and never enabled: nothing is touched.
	m.calls = nil
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || m.index("disable demo") >= 0 || m.index("enable demo") >= 0 {
		t.Fatalf("up = %d, calls = %v", code, m.calls)
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
	if !strings.Contains(errw.String(), service.NoLoginStart) || !u.m.saw("start demo dispatch") {
		t.Fatalf("stderr = %q, calls = %v", errw.String(), u.m.calls)
	}
	work, _, _, _, deps = upFixture(t)
	deps.Services = func() (service.Manager, string) { return nil, "systemd --user is not available" }
	deps.AtLogin = func() bool { return true }
	out.Reset()
	errw.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up in tab mode = %d: %s", code, errw.String())
	}
	if !strings.Contains(errw.String(), service.NoLoginStart) {
		t.Fatalf("tab mode stderr = %q", errw.String())
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
	if !strings.Contains(errw.String(), "enable: refused") || !m.saw("start demo dispatch") {
		t.Fatalf("stderr = %q, calls = %v", errw.String(), m.calls)
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
	if !m.saw("enable demo") || !m.saw("enable other") || strings.Count(strings.Join(m.calls, ";"), "enable demo") != 1 {
		t.Fatalf("calls = %v", m.calls)
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
	if !m.saw("disable demo") || m.saw("disable other") {
		t.Fatalf("calls = %v", m.calls)
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
		t.Fatalf("calls = %v (disable before the install)", m.calls)
	}
	m.calls, m.failDisable = nil, true
	errw.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
		t.Fatal("up succeeded with login start neither read nor disabled")
	}
	if m.index("install demo dispatch") >= 0 || !strings.Contains(errw.String(), "start at login") {
		t.Fatalf("calls = %v, stderr = %q", m.calls, errw.String())
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
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 0 || !m.saw("disable demo") {
		t.Fatalf("code %d, calls %v, stderr %q", code, m.calls, errw.String())
	}
	m.failDisable = true
	if code := runServiceCmd("", []string{"login"}, d, &out, &errw); code != 1 {
		t.Fatalf("a failed disable exited %d", code)
	}
}
