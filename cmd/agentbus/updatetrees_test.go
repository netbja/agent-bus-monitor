package main

import (
	"os"
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// quiet-update-auto §5, the real hooks of agentbus update's prune: the
// holder builds on disk are every project's units and every binary copy
// (a supervised holder has no unit); the collection removes an old holder
// that neither runs nor answers, never the current build, an active one or
// one that answers.
func TestUpdateHolderHooks(t *testing.T) {
	data := t.TempDir()
	old, active, answering, current, copyOnly := service.BuildKey("v1"), service.BuildKey("v2"), service.BuildKey("v3"), service.BuildKey("v4"), service.BuildKey("v5")
	hm := &fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{}, active: map[string]bool{active: true},
		units: []service.HolderUnit{{Project: "a", Key: old, Raw: "v1"}, {Project: "a", Key: active, Raw: "v2"},
			{Project: "b", Key: answering, Raw: "v3"}, {Project: "b", Key: current, Raw: "v4"}}}
	d := serviceCmdDeps{Manager: hm, Paths: service.Paths{DataHome: data},
		HolderAnswers: func(project, key string) bool { return key == answering }}
	if err := os.MkdirAll(service.HolderBinDir(d.Paths, copyOnly), 0o700); err != nil {
		t.Fatal(err)
	}
	keys, err := holderKeys(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{old, active, answering, current, copyOnly} {
		if !keys[k] {
			t.Errorf("%s not among the holder builds: %v", k, keys)
		}
	}
	if err := holderGC(d, current); err != nil {
		t.Fatal(err)
	}
	var left []string
	units, _ := hm.HolderUnits("")
	for _, u := range units {
		left = append(left, u.Key)
	}
	if slices.Contains(left, old) || !slices.Contains(left, active) || !slices.Contains(left, answering) || !slices.Contains(left, current) {
		t.Fatalf("units left %v", left)
	}
}
