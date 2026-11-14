package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/service"
)

// fakeHolders is a HolderManager over a list of units and their activity.
type fakeHolders struct {
	*fakeManager
	units  []service.HolderUnit
	active map[string]bool
	err    error
}

func (f *fakeHolders) InstallHolder(service.HolderSpec) (bool, error) { return false, nil }
func (f *fakeHolders) StartHolder(project, key string) error {
	f.calls = append(f.calls, "start-holder "+project+" "+key)
	return nil
}
func (f *fakeHolders) StopHolder(string, string) error    { return nil }
func (f *fakeHolders) RestartHolder(string, string) error { return nil }
func (f *fakeHolders) HolderActive(project, key string) (bool, error) {
	return f.active[key], f.err
}
func (f *fakeHolders) RemoveHolder(project, key string) error {
	f.calls = append(f.calls, "remove-holder "+project+" "+key)
	for i, u := range f.units {
		if u.Key == key {
			f.units = append(f.units[:i], f.units[i+1:]...)
			break
		}
	}
	return nil
}
func (f *fakeHolders) HolderUnits(project string) ([]service.HolderUnit, error) {
	var out []service.HolderUnit
	for _, u := range f.units {
		if project == "" || u.Project == project {
			out = append(out, u)
		}
	}
	return out, nil
}

// The evidence merges, per build key, the units (and whether they run), the
// sockets that answer (with their terminals and age) and the binary copies.
func TestHolderEvidence(t *testing.T) {
	root := t.TempDir()
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	cur, old, gone, orphan := service.BuildKey("v3"), service.BuildKey("v2"), service.BuildKey("v1"), service.BuildKey("v0")
	for raw, key := range map[string]string{"v3": cur, "v1": gone, "v0": orphan} { // v2's binary copy is already gone
		dir := service.HolderBinDir(paths, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "build-raw"), []byte(raw+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hm := &fakeHolders{fakeManager: newFakeManager(), active: map[string]bool{cur: true, old: true},
		units: []service.HolderUnit{{Project: "demo", Key: cur, Raw: "v3"}, {Project: "demo", Key: old, Raw: "v2"},
			{Project: "other", Key: gone, Raw: "v1"}}}
	probe := holderProbe{
		sockets: func(project string) (map[string]time.Duration, error) {
			return map[string]time.Duration{cur: time.Minute, old: 30 * time.Hour}, nil
		},
		answer: func(project, key string) (int, bool) {
			switch key {
			case cur:
				return 1, true
			case old:
				return 2, true
			}
			return 0, false
		},
	}
	e := holderEvidence(hm, paths, "demo", "v3", probe)
	if !e.Known || len(e.Builds) != 3 { // v3, v2, v0
		t.Fatalf("%+v", e)
	}
	by := map[string]health.HolderBuild{}
	for _, b := range e.Builds {
		by[b.Raw] = b
	}
	if b := by["v3"]; !b.Current || !b.Unit || !b.Active || !b.Answers || b.Terms != 1 || !b.Binary {
		t.Errorf("v3 %+v", b)
	}
	if b := by["v2"]; b.Current || !b.Unit || !b.Answers || b.Terms != 2 || b.Since != 30*time.Hour || b.Binary {
		t.Errorf("v2 %+v", b)
	}
	// v1's binary copy is run by another project's unit: never this project's leftover.
	if _, ok := by["v1"]; ok {
		t.Errorf("v1, another project's build, listed: %+v", by["v1"])
	}
	// v0's copy, recorded by no unit at all, is a leftover.
	if b := by["v0"]; b.Unit || b.Answers || !b.Binary {
		t.Errorf("v0 %+v", b)
	}
	if got := health.EvaluateHolderLeftovers(e); got.Status != health.Warn || !strings.Contains(got.Detail, "v0") || strings.Contains(got.Detail, "v1") {
		t.Errorf("leftovers %+v", got)
	}
	if got := health.EvaluateHolderDraining(e); got.Status != health.Warn || !strings.Contains(got.Detail, "v2") {
		t.Errorf("draining %+v", got)
	}

	// A manager that cannot say is unknown, never "no holder".
	hm.err = errors.New("systemctl failed")
	if e := holderEvidence(hm, paths, "demo", "v3", probe); e.Known {
		t.Fatal("an unreadable manager read as known")
	}
}

// service start holder (sentinel's one action on a holder crit, spec 4.4a)
// starts the current build's installed unit, never another; with none
// installed it says so.
func TestServiceStartHolder(t *testing.T) {
	cur := service.BuildKey("v3")
	hm := &fakeHolders{fakeManager: newFakeManager(), active: map[string]bool{},
		units: []service.HolderUnit{{Project: "demo", Key: cur, Raw: "v3"}, {Project: "demo", Key: service.BuildKey("v2"), Raw: "v2"}}}
	d := serviceCmdDeps{Manager: hm, HolderBuild: func() string { return "v3" }}
	var out, errw strings.Builder
	if code := runServiceCmd("demo", []string{"start", "holder"}, d, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if strings.Join(hm.calls, ",") != "start-holder demo "+cur {
		t.Fatalf("calls %v", hm.calls)
	}
	d.HolderBuild = func() string { return "v4" }
	hm.calls = nil
	if code := runServiceCmd("demo", []string{"start", "holder"}, d, &out, &errw); code == 0 || len(hm.calls) != 0 || !strings.Contains(errw.String(), "agentbus up") {
		t.Fatalf("no unit of the current build: calls %v err %q", hm.calls, errw.String())
	}
}

// service gc removes the project's older builds that no longer run (unit and
// binary), then the binary copies no unit records; the current build, a
// running one and an answering one stay.
func TestServiceGC(t *testing.T) {
	root := t.TempDir()
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	keys := map[string]string{}
	for _, raw := range []string{"v3", "v2", "v1", "v0"} {
		keys[raw] = service.BuildKey(raw)
		src := filepath.Join(t.TempDir(), "agentbus")
		if err := os.WriteFile(src, []byte(raw), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := service.InstallHolderBinary(paths, raw, src); err != nil {
			t.Fatal(err)
		}
	}
	// v3 current; v2 running; v1 stopped (removed); v0 a binary with no unit (removed).
	hm := &fakeHolders{fakeManager: newFakeManager(), active: map[string]bool{keys["v2"]: true},
		units: []service.HolderUnit{{Project: "demo", Key: keys["v3"], Raw: "v3"}, {Project: "demo", Key: keys["v2"], Raw: "v2"},
			{Project: "demo", Key: keys["v1"], Raw: "v1"}}}
	d := serviceCmdDeps{Manager: hm, Paths: paths, HolderBuild: func() string { return "v3" },
		HolderAnswers: func(project, key string) bool { return false }, HolderAnswersAny: func(key string) bool { return false }}
	var out, errw strings.Builder
	if code := runServiceCmd("demo", []string{"gc"}, d, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	for raw, want := range map[string]bool{"v3": true, "v2": true, "v1": false, "v0": false} {
		if _, err := os.Stat(service.HolderBinDir(paths, keys[raw])); (err == nil) != want {
			t.Errorf("%s binary: present %v, want %v", raw, err == nil, want)
		}
	}
	if !hm.saw("remove-holder demo "+keys["v1"]) || hm.saw("remove-holder demo "+keys["v2"]) || hm.saw("remove-holder demo "+keys["v3"]) {
		t.Errorf("calls %v", hm.calls)
	}
	if !strings.Contains(out.String(), "v1") || !strings.Contains(out.String(), "removed") {
		t.Errorf("out %q", out.String())
	}
}
