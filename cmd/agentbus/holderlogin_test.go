package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
)

// fakeLoginHolder is a manager with a login backend and the holder's units
// (holder hardening H1).
type fakeLoginHolder struct {
	*fakeHolderManager
	enabled map[string]bool
}

func newFakeLoginHolder(m *fakeManager) *fakeLoginHolder {
	return &fakeLoginHolder{fakeHolderManager: &fakeHolderManager{fakeManager: m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}},
		enabled: map[string]bool{}}
}

func (f *fakeLoginHolder) Enable(p string) error {
	f.calls = append(f.calls, "enable "+p)
	f.enabled[p] = true
	return nil
}
func (f *fakeLoginHolder) Disable(p string) error {
	f.calls = append(f.calls, "disable "+p)
	f.enabled[p] = false
	return nil
}
func (f *fakeLoginHolder) Enabled(p string) (bool, error) { return f.enabled[p], nil }
func (f *fakeLoginHolder) EnableHolder(p, key string) error {
	f.calls = append(f.calls, "enable-holder "+p+" "+key)
	return nil
}
func (f *fakeLoginHolder) DisableHolders(p string) error {
	f.calls = append(f.calls, "disable-holders "+p)
	return nil
}

func (f *fakeLoginHolder) at(c string) int { return slices.Index(f.calls, c) }

// H1: up with start at login on enables the current build's holder with the
// other services, after its unit is installed; off, every holder of the
// project is disabled.
func TestUpEnablesTheHolderAtLogin(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	exe := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u.exe = exe
	m := newFakeLoginHolder(u.m)
	deps.Services = func() (service.Manager, string) { return m, "" }
	raw := "v9.9.9+abc"
	deps.HolderBuild = func() string { return raw }
	deps.HolderAnswers = func(string, string) bool { return true }
	deps.Sleep = func(time.Duration) {}
	deps.AtLogin = func() bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	enable := "enable-holder demo " + service.BuildKey(raw)
	if i := m.at(enable); i < 0 || i < m.at("enable demo") {
		t.Fatalf("calls %v: the holder not enabled with the services", m.calls)
	}
	if !strings.Contains(out.String(), "holder: starts at login") {
		t.Fatalf("out %q", out.String())
	}
	m.calls = nil
	deps.AtLogin = func() bool { return false }
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if m.at("disable-holders demo") < 0 || m.at(enable) >= 0 {
		t.Fatalf("off: calls %v", m.calls)
	}
}

// H1: agentbus service login applies the option to each project's current
// holder too.
func TestServiceLoginEnablesTheHolder(t *testing.T) {
	m := newFakeLoginHolder(newFakeManager())
	if _, err := m.Install(service.Spec{Project: "demo", Kind: service.Dispatch}); err != nil {
		t.Fatal(err)
	}
	raw := "v9.9.9+abc"
	m.units = []service.HolderUnit{{Project: "demo", Key: service.BuildKey(raw), Raw: raw}}
	on := true
	d := serviceCmdDeps{Manager: m, AtLogin: func() bool { return on }, HolderBuild: func() string { return raw }}
	var out, errw bytes.Buffer
	if code := serviceLogin(d, &out, &errw); code != 0 {
		t.Fatalf("login = %d: %s", code, errw.String())
	}
	if m.at("enable-holder demo "+service.BuildKey(raw)) < 0 {
		t.Fatalf("on: calls %v", m.calls)
	}
	// This build's holder not installed for the project: said, never enabled.
	m.units, m.calls = nil, nil
	out.Reset()
	if code := serviceLogin(d, &out, &errw); code != 0 || m.at("enable-holder demo "+service.BuildKey(raw)) >= 0 ||
		!strings.Contains(out.String(), "no holder of this build") {
		t.Fatalf("no unit: code %d, calls %v, out %q", code, m.calls, out.String())
	}
	m.calls = nil
	m.enabled["demo"] = true
	on = false
	if code := serviceLogin(d, &out, &errw); code != 0 || m.at("disable-holders demo") < 0 {
		t.Fatalf("off: code %d, calls %v", code, m.calls)
	}
}
