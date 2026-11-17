package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installHolders installs two builds' holder units of demo under m.
func installHolders(t *testing.T, m HolderManager, p Paths, raws ...string) {
	t.Helper()
	for _, raw := range raws {
		if _, err := InstallHolderBinary(p, raw, fakeExe(t, raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := m.InstallHolder(HolderSpec{Project: "demo", Raw: raw, Paths: p, EnvFile: "/e/demo.env", WorkDir: "/w"}); err != nil {
			t.Fatal(err)
		}
	}
}

// H1 (holder hardening §3): with start at login on, the current build's
// holder unit gains [Install] and is enabled; every other build of the
// project is disabled and loses it (it drains, never started again). Off,
// every build is disabled. Nothing is started or stopped.
func TestSystemdHolderAtLogin(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	installHolders(t, m, p, "v1", "v2")
	unit := func(raw string) string {
		return string(mustReadFile(t, filepath.Join(p.ConfigHome, "systemd", "user", HolderUnitName("demo", BuildKey(raw)))))
	}
	var hl HolderLoginStart = m
	f.calls = nil
	if err := hl.EnableHolder("demo", BuildKey("v1")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(unit("v1"), systemdInstall) || strings.Contains(unit("v2"), "[Install]") {
		t.Fatalf("v1:\n%s\nv2:\n%s", unit("v1"), unit("v2"))
	}
	if !f.saw("--user enable "+HolderUnitName("demo", BuildKey("v1"))) || !f.saw("--user disable "+HolderUnitName("demo", BuildKey("v2"))) {
		t.Fatalf("calls %v", f.calls)
	}
	// The next build: the enable follows it.
	f.calls = nil
	if err := hl.EnableHolder("demo", BuildKey("v2")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unit("v1"), "[Install]") || !strings.HasSuffix(unit("v2"), systemdInstall) {
		t.Fatalf("after v2: v1:\n%s\nv2:\n%s", unit("v1"), unit("v2"))
	}
	if !f.saw("--user disable "+HolderUnitName("demo", BuildKey("v1"))) || !f.saw("--user enable "+HolderUnitName("demo", BuildKey("v2"))) {
		t.Fatalf("calls %v", f.calls)
	}
	f.calls = nil
	if err := hl.DisableHolders("demo"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unit("v1")+unit("v2"), "[Install]") {
		t.Fatal("a holder keeps its [Install] after the option went off")
	}
	for _, c := range f.calls {
		if strings.Contains(c, " start ") || strings.Contains(c, " stop ") || strings.Contains(c, "restart") || strings.Contains(c, "--now") {
			t.Fatalf("a holder was started or stopped: %v", f.calls)
		}
	}
	// A unit rewritten by a later install keeps the enable once re-applied.
	if err := hl.EnableHolder("demo", BuildKey("v2")); err != nil {
		t.Fatal(err)
	}
	if strings.Count(unit("v2"), "[Install]") != 1 {
		t.Fatalf("twice enabled:\n%s", unit("v2"))
	}
}

func TestLaunchdHolderAtLogin(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{out: map[string]fakeOut{"print gui/": {text: "Could not find service", failed: true}}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	installHolders(t, m, p, "v1", "v2")
	agent := func(raw string) string {
		return filepath.Join(p.Home, "Library", "LaunchAgents", HolderLabel("demo", BuildKey(raw))+".plist")
	}
	var hl HolderLoginStart = m
	if err := hl.EnableHolder("demo", BuildKey("v1")); err != nil {
		t.Fatal(err)
	}
	if b := string(mustReadFile(t, agent("v1"))); !strings.Contains(b, "<key>RunAtLoad</key><true/>") {
		t.Fatalf("copy:\n%s", b)
	}
	if _, err := os.Stat(agent("v2")); !os.IsNotExist(err) {
		t.Fatalf("v2 copied: %v", err)
	}
	if err := hl.EnableHolder("demo", BuildKey("v2")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent("v1")); !os.IsNotExist(err) {
		t.Fatalf("v1 kept after v2: %v", err)
	}
	if err := hl.DisableHolders("demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent("v2")); !os.IsNotExist(err) {
		t.Fatalf("v2 kept after off: %v", err)
	}
	if f.saw("bootout") || f.saw("bootstrap") || f.saw("kickstart") {
		t.Fatalf("a holder was started or stopped: %v", f.calls)
	}
	// gc's remove takes the copy too.
	if err := hl.EnableHolder("demo", BuildKey("v1")); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveHolder("demo", BuildKey("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent("v1")); !os.IsNotExist(err) {
		t.Fatalf("remove left the copy: %v", err)
	}
}
