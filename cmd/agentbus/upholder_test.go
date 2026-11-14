package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// fakeHolderManager is a fakeManager that also holds holder units.
type fakeHolderManager struct {
	*fakeManager
	holders map[string]service.HolderSpec // by key
	units   []service.HolderUnit
	active  map[string]bool
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
