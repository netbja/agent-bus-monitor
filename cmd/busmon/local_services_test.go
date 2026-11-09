package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// reviewer-codex: a manager that cannot be checked is never "no unit here":
// the quiet check refuses, and only an explicit none is quiet.
func TestServicesActive(t *testing.T) {
	down := func() (service.Manager, error) { return nil, errors.New("systemd --user is not available") }
	if _, err := servicesActive("old", down); err == nil || !strings.Contains(err.Error(), "systemd --user") {
		t.Fatalf("an unreachable manager: %v", err)
	}
	none := func() (service.Manager, error) { return nil, nil }
	if active, err := servicesActive("old", none); err != nil || active {
		t.Fatalf("no manager by choice: %v %v", active, err)
	}
	running := func() (service.Manager, error) {
		return fakeManager{active: map[string]bool{"old/missions": true}}, nil
	}
	if active, err := servicesActive("old", running); err != nil || !active {
		t.Fatalf("a running unit: %v %v", active, err)
	}
	if active, err := servicesActive("new", running); err != nil || active {
		t.Fatalf("another project's unit: %v %v", active, err)
	}
}

// The delete's removal: an unreachable manager is an error (so nothing is
// deleted), an explicit none removes nothing, a manager removes the
// project's units and its environment file.
func TestRemoveUnits(t *testing.T) {
	home := t.TempDir()
	getenv := func(k string) string { return map[string]string{"HOME": home}[k] }
	paths, err := service.PathsFrom(getenv)
	if err != nil {
		t.Fatal(err)
	}
	envFile := paths.EnvFile("old")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	down := func() (service.Manager, error) { return nil, errors.New("launchd has no GUI session") }
	if kinds, err := removeUnits("old", getenv, down); err == nil || kinds != nil || !strings.Contains(err.Error(), "launchd") {
		t.Fatalf("an unreachable manager: %v %v", kinds, err)
	}
	if kinds, err := removeUnits("old", getenv, func() (service.Manager, error) { return nil, nil }); err != nil || kinds != nil {
		t.Fatalf("no manager by choice: %v %v", kinds, err)
	}
	if _, err := os.Stat(envFile); err != nil {
		t.Fatalf("the environment file went before any manager removed a unit: %v", err)
	}
	m := fakeManager{units: []service.Unit{{Project: "old", Kind: "dispatch"}, {Project: "new", Kind: "dispatch"}, {Project: "old", Kind: "missions"}}}
	kinds, err := removeUnits("old", getenv, func() (service.Manager, error) { return m, nil })
	if err != nil || !slices.Equal(kinds, []string{"dispatch", "missions"}) {
		t.Fatalf("removed %v %v", kinds, err)
	}
	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Fatalf("the environment file stayed: %v", err)
	}
}
