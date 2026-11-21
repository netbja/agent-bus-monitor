package main

import (
	"errors"
	"io"
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

// unitsFail is a holder manager whose unit list cannot be read.
type unitsFail struct{ *fakeHolderManager }

func (unitsFail) HolderUnits(string) ([]service.HolderUnit, error) {
	return nil, errors.New("unit directory unreadable")
}

// foureyes: a unit list that cannot be read is an error (the prune then keeps
// every tree), never an empty set of holder builds.
func TestUpdateHolderKeysUnitsUnreadable(t *testing.T) {
	hm := unitsFail{&fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{}, active: map[string]bool{}}}
	d := serviceCmdDeps{Manager: hm, Paths: service.Paths{DataHome: t.TempDir()}}
	if keys, err := holderKeys(d); err == nil {
		t.Fatalf("no error, keys %v", keys)
	}
}

// foureyes: agentbus update's real deps carry both hooks; without them the
// prune keeps every tree and silently never removes one.
func TestRealUpdateDepsPrune(t *testing.T) {
	d := realUpdateDeps("", io.Discard, io.Discard)
	if d.HolderKeys == nil || d.HolderGC == nil {
		t.Fatalf("HolderKeys %v, HolderGC %v", d.HolderKeys != nil, d.HolderGC != nil)
	}
}
