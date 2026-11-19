package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

// service start holder (sentinel's one action on a holder crit, spec 4.4a)
// starts the current build's installed unit, never another; with none
// installed it says so.
func TestServiceStartHolder(t *testing.T) {
	cur := service.BuildKey("v3")
	hm := &cmdtest.Holders{Manager: newFakeManager(), Active: map[string]bool{},
		Units: []service.HolderUnit{{Project: "demo", Key: cur, Raw: "v3"}, {Project: "demo", Key: service.BuildKey("v2"), Raw: "v2"}}}
	d := serviceCmdDeps{Manager: hm, HolderBuild: func() string { return "v3" }}
	var out, errw strings.Builder
	if code := runServiceCmd("demo", []string{"start", "holder"}, d, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if strings.Join(hm.Calls, ",") != "start-holder demo "+cur {
		t.Fatalf("calls %v", hm.Calls)
	}
	d.HolderBuild = func() string { return "v4" }
	hm.Calls = nil
	if code := runServiceCmd("demo", []string{"start", "holder"}, d, &out, &errw); code == 0 || len(hm.Calls) != 0 || !strings.Contains(errw.String(), "agentbus up") {
		t.Fatalf("no unit of the current build: calls %v err %q", hm.Calls, errw.String())
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
	hm := &cmdtest.Holders{Manager: newFakeManager(), Active: map[string]bool{keys["v2"]: true},
		Units: []service.HolderUnit{{Project: "demo", Key: keys["v3"], Raw: "v3"}, {Project: "demo", Key: keys["v2"], Raw: "v2"},
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
	if !hm.Saw("remove-holder demo "+keys["v1"]) || hm.Saw("remove-holder demo "+keys["v2"]) || hm.Saw("remove-holder demo "+keys["v3"]) {
		t.Errorf("calls %v", hm.Calls)
	}
	if !strings.Contains(out.String(), "v1") || !strings.Contains(out.String(), "removed") {
		t.Errorf("out %q", out.String())
	}
}

// A build shared with another project keeps its binary when this project's
// unit goes: gc says so, never "binary removed" (reviewer-codex).
func TestServiceGCSaysASharedCopyIsKept(t *testing.T) {
	root := t.TempDir()
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	shared := service.BuildKey("v1")
	src := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(src, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallHolderBinary(paths, "v1", src); err != nil {
		t.Fatal(err)
	}
	hm := &cmdtest.Holders{Manager: newFakeManager(), Active: map[string]bool{},
		Units: []service.HolderUnit{{Project: "demo", Key: shared, Raw: "v1"}, {Project: "other", Key: shared, Raw: "v1"}}}
	d := serviceCmdDeps{Manager: hm, Paths: paths, HolderBuild: func() string { return "v2" },
		HolderAnswers: func(string, string) bool { return false }, HolderAnswersAny: func(string) bool { return false }}
	var out, errw strings.Builder
	if code := runServiceCmd("demo", []string{"gc"}, d, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if _, err := os.Stat(service.HolderBinDir(paths, shared)); err != nil {
		t.Fatalf("the shared copy was removed: %v", err)
	}
	if strings.Contains(out.String(), "binary removed") || !strings.Contains(out.String(), "binary kept") {
		t.Fatalf("out %q", out.String())
	}
}
