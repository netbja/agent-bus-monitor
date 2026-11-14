package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// fakeHolderManager is a fakeManager that also holds holder units.
type fakeHolderManager struct {
	*fakeManager
	holders map[string]service.HolderSpec // by key
	units    []service.HolderUnit
	active   map[string]bool
	failStop map[string]bool // StopHolder fails for these keys
}

func (f *fakeHolderManager) InstallHolder(s service.HolderSpec) (bool, error) {
	key := service.BuildKey(s.Raw)
	f.calls = append(f.calls, "install-holder "+s.Project+" "+key)
	f.holders[key] = s
	for _, u := range f.units {
		if u.Project == s.Project && u.Key == key {
			return false, nil
		}
	}
	f.units = append(f.units, service.HolderUnit{Project: s.Project, Key: key, Raw: s.Raw})
	return true, nil
}
func (f *fakeHolderManager) StartHolder(project, key string) error {
	f.calls = append(f.calls, "start-holder "+project+" "+key)
	f.active[key] = true
	return nil
}
func (f *fakeHolderManager) HolderActive(project, key string) (bool, error) { return f.active[key], nil }
func (f *fakeHolderManager) RemoveHolder(project, key string) error {
	f.calls = append(f.calls, "remove-holder "+project+" "+key)
	for i, u := range f.units {
		if u.Project == project && u.Key == key {
			f.units = append(f.units[:i], f.units[i+1:]...)
			break
		}
	}
	return nil
}
func (f *fakeHolderManager) HolderUnits(project string) ([]service.HolderUnit, error) {
	var out []service.HolderUnit
	for _, u := range f.units {
		if project == "" || u.Project == project {
			out = append(out, u)
		}
	}
	return out, nil
}

// up installs this build's holder beside the services: the binary copy, the
// unit with the project's env file and working directory, its start, then
// the gc of an older build that no longer runs (spec 4.4a). A running older
// build is kept and never stopped.
func TestUpInstallsAndStartsTheHolder(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	exe := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u.exe = exe
	gone, running := service.BuildKey("v1"), service.BuildKey("v2")
	hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{running: true},
		units: []service.HolderUnit{{Project: "demo", Key: gone, Raw: "v1"}, {Project: "demo", Key: running, Raw: "v2"}}}
	deps.Services = func() (service.Manager, string) { return hm, "" }
	deps.Environ = append(deps.Environ, "ROLES_TOML=/m/roles.toml", "AGENTBUS_REPO=/r")
	env0 := deps.Env
	deps.Env = func(k string) string {
		if k == "ROLES_TOML" {
			return "/m/roles.toml"
		}
		return env0(k)
	}
	raw := "v9.9.9+abc"
	deps.HolderBuild = func() string { return raw }
	deps.HolderAnswers = func(project, key string) bool { return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	key := service.BuildKey(raw)
	s, ok := hm.holders[key]
	if !ok || s.Project != "demo" || s.Raw != raw || s.EnvFile != u.paths.EnvFile("demo") || s.WorkDir != work {
		t.Fatalf("holder spec %+v (installed: %v)", s, ok)
	}
	// The directory is in the env file too: a holder already running reads it
	// at its next Start (reviewer-codex).
	env, err := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if err != nil || env[service.HolderDirEnv] != work || env[service.HolderManifestEnv] != "/m/roles.toml" {
		t.Fatalf("env file %v %v", env, err)
	}
	// Under the holder's own names only: the env file is every service's, and
	// ROLES_TOML or AGENTBUS_REPO would pin the dispatcher too (foureyes).
	for _, k := range []string{"ROLES_TOML", "AGENTBUS_REPO"} {
		if _, ok := env[k]; ok {
			t.Fatalf("%s in the shared env file: %v", k, env)
		}
	}
	if b, err := os.ReadFile(filepath.Join(service.HolderBinDir(u.paths, key), "agentbus")); err != nil || string(b) != "#!/bin/sh\n" {
		t.Fatalf("the binary copy: %q %v", b, err)
	}
	calls := strings.Join(hm.calls, "\n")
	for _, c := range []string{"install-holder demo " + key, "start-holder demo " + key, "remove-holder demo " + gone} {
		if !strings.Contains(calls, c) {
			t.Fatalf("missing %q in\n%s", c, calls)
		}
	}
	if strings.Index(calls, "start-holder") > strings.Index(calls, "remove-holder") {
		t.Fatalf("the gc ran before the new build started:\n%s", calls)
	}
	if strings.Contains(calls, "remove-holder demo "+running) {
		t.Fatalf("a running build was touched:\n%s", calls)
	}
	if !strings.Contains(out.String(), "holder: ") {
		t.Fatalf("out = %q", out.String())
	}
}

// A manager without holder units (or no build id) installs no holder.
func TestUpWithoutAHolderManagerInstallsNoHolder(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.HolderBuild = func() string { return "v1" }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if _, err := os.Stat(filepath.Join(u.paths.DataHome, "agentbus", "holder")); !os.IsNotExist(err) {
		t.Fatalf("a holder binary was copied: %v", err)
	}
}

func (f *fakeHolderManager) StopHolder(project, key string) error {
	f.calls = append(f.calls, "stop-holder "+project+" "+key)
	if f.failStop[key] {
		return errors.New("manager error")
	}
	f.active[key] = false
	return nil
}

func (f *fakeHolderManager) RestartHolder(project, key string) error {
	f.calls = append(f.calls, "restart-holder "+project+" "+key)
	return nil
}

// `service restart holder` kills every agent the holder holds: refused
// unless --kill-agents is given, and it says how many (spec 4.4a).
func TestServiceRestartHolderNeedsKillAgents(t *testing.T) {
	raw := "v9"
	key := service.BuildKey(raw)
	newDeps := func(terms int, err error) (serviceCmdDeps, *fakeHolderManager) {
		hm := &fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{}, active: map[string]bool{key: true},
			units: []service.HolderUnit{{Project: "demo", Key: key, Raw: raw}}}
		return serviceCmdDeps{Manager: hm, HolderBuild: func() string { return raw },
			HolderTerms: func(project, k string) (int, error) {
				if project != "demo" || k != key {
					t.Errorf("counted %s %s", project, k)
				}
				return terms, err
			}}, hm
	}
	d, hm := newDeps(3, nil)
	var out, errw bytes.Buffer
	if code := runServiceCmd("demo", []string{"restart", "holder"}, d, &out, &errw); code == 0 {
		t.Fatalf("restarted without --kill-agents: %s", out.String())
	}
	if !strings.Contains(errw.String(), "3 agent") || !strings.Contains(errw.String(), "--kill-agents") {
		t.Fatalf("stderr %q", errw.String())
	}
	if strings.Contains(strings.Join(hm.calls, " "), "restart-holder") {
		t.Fatalf("restarted: %v", hm.calls)
	}

	d, hm = newDeps(0, errors.New("no holder answers"))
	errw.Reset()
	if code := runServiceCmd("demo", []string{"restart", "holder"}, d, &out, &errw); code == 0 || strings.Contains(strings.Join(hm.calls, " "), "restart-holder") {
		t.Fatalf("an uncounted holder restarted: %v", hm.calls)
	}
	if !strings.Contains(errw.String(), "no holder answers") {
		t.Fatalf("stderr %q", errw.String())
	}

	d, hm = newDeps(3, nil)
	out.Reset()
	if code := runServiceCmd("demo", []string{"restart", "holder", "--kill-agents"}, d, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !hm.saw("restart-holder demo " + key) {
		t.Fatalf("calls %v", hm.calls)
	}
	if !strings.Contains(out.String(), "holder: restarted") {
		t.Fatalf("out %q", out.String())
	}
}

// holderSystemd is the real systemd manager over a fake systemctl that keeps
// which holder units run: stop makes one inactive.
func holderSystemd(t *testing.T) (service.Systemd, service.Paths, map[string]bool, *[]string) {
	t.Helper()
	root := t.TempDir()
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	active := map[string]bool{}
	calls := &[]string{}
	run := func(args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if len(args) == 3 && args[1] == "is-active" {
			if active[args[2]] {
				return []byte("active\n"), nil
			}
			return []byte("inactive\n"), errors.New("exit status 3")
		}
		if len(args) == 3 && args[1] == "stop" {
			active[args[2]] = false
		}
		return nil, nil
	}
	return service.Systemd{Run: run, Paths: paths}, paths, active, calls
}

func installTestHolder(t *testing.T, m service.Systemd, paths service.Paths, active map[string]bool, project, raw string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallHolderBinary(paths, raw, src); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallHolder(service.HolderSpec{Project: project, Raw: raw, Paths: paths, EnvFile: "/e/" + project + ".env", WorkDir: "/w"}); err != nil {
		t.Fatal(err)
	}
	key := service.BuildKey(raw)
	active[service.HolderUnitName(project, key)] = true
	return key
}

// Every path that lists, stops or removes services sees the holder units
// (foureyes): service list shows them, down stops the project's, remove
// removes the project's units and the binaries no other project runs, and
// remove --all (setup uninstall) leaves none. On the real systemd manager,
// a project with a '-' in its name included.
func TestHolderUnitsOnTheRemovalPaths(t *testing.T) {
	m, paths, active, calls := holderSystemd(t)
	k1 := installTestHolder(t, m, paths, active, "demo", "v1")
	k2 := installTestHolder(t, m, paths, active, "demo", "v2")
	installTestHolder(t, m, paths, active, "agent-bus-monitor", "v1")
	d := serviceCmdDeps{Manager: m, Paths: paths, Bus: func(string) (*bus.Bus, error) { return nil, errors.New("no broker") }}

	var out, errw bytes.Buffer
	if code := runServiceCmd("", []string{"list"}, d, &out, &errw); code != 0 {
		t.Fatalf("list: %d %s", code, errw.String())
	}
	for _, want := range []string{"demo                     holder    active, build v1", "demo                     holder    active, build v2",
		"agent-bus-monitor        holder    active, build v1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if code := runDown([]string{"demo"}, d, &out, &errw); code != 0 {
		t.Fatalf("down: %d %s", code, errw.String())
	}
	for _, k := range []string{k1, k2} {
		if active[service.HolderUnitName("demo", k)] {
			t.Fatalf("down left %s running: %v", k, *calls)
		}
	}
	if !active[service.HolderUnitName("agent-bus-monitor", k1)] {
		t.Fatal("down demo stopped another project's holder")
	}
	if !strings.Contains(out.String(), "holder (build v1): stopped") {
		t.Fatalf("down: %s", out.String())
	}
	// Stopping a holder stops the agents it runs: down says so, never that
	// they stay open (reviewer-codex).
	if !strings.Contains(out.String(), "the agents the holder ran were stopped with it") || strings.Contains(out.String(), "The agents and the herdr workspace stay open") {
		t.Fatalf("down: %s", out.String())
	}

	out.Reset()
	if code := runServiceCmd("demo", []string{"remove"}, d, &out, &errw); code != 0 {
		t.Fatalf("remove: %d %s", code, errw.String())
	}
	if u, _ := m.HolderUnits("demo"); len(u) != 0 {
		t.Fatalf("remove kept %+v", u)
	}
	if _, err := os.Stat(service.HolderBinDir(paths, k2)); !os.IsNotExist(err) {
		t.Fatalf("v2's binary kept: %v", err)
	}
	if _, err := os.Stat(service.HolderBinDir(paths, k1)); err != nil {
		t.Fatalf("v1's binary, still run by agent-bus-monitor, removed: %v", err)
	}

	out.Reset()
	if code := runServiceCmd("", []string{"remove", "--all"}, d, &out, &errw); code != 0 {
		t.Fatalf("remove --all: %d %s", code, errw.String())
	}
	if u, _ := m.HolderUnits(""); len(u) != 0 {
		t.Fatalf("remove --all kept %+v", u)
	}
	if active[service.HolderUnitName("agent-bus-monitor", k1)] {
		t.Fatal("remove --all left a holder running")
	}
	if _, err := os.Stat(service.HolderBinDir(paths, k1)); !os.IsNotExist(err) {
		t.Fatalf("v1's binary kept: %v", err)
	}
}

// A holder that could not be stopped is never reported as stopped: down
// names it, and its agents may still run (reviewer-codex).
func TestDownSaysWhichHoldersDidNotStop(t *testing.T) {
	k1, k2 := service.BuildKey("v1"), service.BuildKey("v2")
	hm := &fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{},
		active: map[string]bool{k1: true, k2: true}, failStop: map[string]bool{k2: true},
		units: []service.HolderUnit{{Project: "demo", Key: k1, Raw: "v1"}, {Project: "demo", Key: k2, Raw: "v2"}}}
	d := serviceCmdDeps{Manager: hm, Bus: func(string) (*bus.Bus, error) { return nil, errors.New("no broker") }}
	var out, errw bytes.Buffer
	if code := runDown([]string{"demo"}, d, &out, &errw); code == 0 {
		t.Fatal("a failed stop exited 0")
	}
	if !strings.Contains(out.String(), "holder (build v1): stopped") || !strings.Contains(errw.String(), "holder (build v2): manager error") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}
	if strings.Contains(out.String(), "the agents the holder ran were stopped with it") || !strings.Contains(out.String(), "1 holder could not be stopped (build v2)") {
		t.Fatalf("summary %q", out.String())
	}
}
