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

// resource-limits §12.5, step 4b-2: the holder's scopes from the host's
// systemd-run and systemctl, cap B from config.toml at each launch, and
// another build's holder judged active when its socket answers or its unit
// runs.

type scopeHolderManager struct {
	service.HolderManager
	active map[string]bool
	err    error
	asked  []string
}

func (m *scopeHolderManager) HolderActive(project, key string) (bool, error) {
	m.asked = append(m.asked, project+"/"+key)
	return m.active[key], m.err
}

func scopeTestDeps(t *testing.T) (holderScopeDeps, *scopeHolderManager) {
	t.Helper()
	home := t.TempDir()
	m := &scopeHolderManager{active: map[string]bool{}}
	return holderScopeDeps{
		goos:    "linux",
		look:    func(b string) (string, error) { return "/usr/bin/" + b, nil },
		self:    "/data/agentbus/holder/k/agentbus",
		getenv:  func(k string) string { return map[string]string{"XDG_CONFIG_HOME": home}[k] },
		answers: func(project, key string) bool { return key == "answers.0123456789" },
		manager: func() (service.HolderManager, error) { return m, nil },
	}, m
}

func TestHolderScopesArgv(t *testing.T) {
	d, _ := scopeTestDeps(t)
	sc, why := holderScopes("demo", d)
	if sc == nil {
		t.Fatalf("no scopes: %s", why)
	}
	got := sc.Argv("agentbus-demo-k.0123456789-coder-t1.scope", []string{"claude", "--resume", "x"})
	want := []string{"/usr/bin/systemd-run", "--user", "--scope", "--quiet", "--slice=agentbus.slice", "--unit=agentbus-demo-k.0123456789-coder-t1.scope",
		"-p", "Delegate=yes", "-p", "MemoryMax=30%", "-p", "MemorySwapMax=2G", "-p", "OOMPolicy=continue",
		"--", "/data/agentbus/holder/k/agentbus", "agent-enter", "--", "claude", "--resume", "x"}
	if !slices.Equal(got, want) {
		t.Errorf("argv\n%q\nwant\n%q", got, want)
	}
	// config.toml is read at each launch: the person's later edit applies to
	// the next terminal; "off" sets none.
	dir := filepath.Join(d.getenv("XDG_CONFIG_HOME"), "agentbus")
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[limits]\nagent_memory_max = \"8G\"\nagent_swap_max = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := strings.Join(sc.Argv("u.scope", []string{"c"}), " ")
	if !strings.Contains(a, "MemoryMax=8G") || strings.Contains(a, "MemorySwapMax") {
		t.Errorf("after the edit: %s", a)
	}
}

func TestHolderScopesNeedTheirTools(t *testing.T) {
	for _, tc := range []struct {
		name, goos, missing, why string
	}{
		{"macOS", "darwin", "", "not Linux"},
		{"no systemd-run", "linux", "systemd-run", "systemd-run"},
		{"no systemctl", "linux", "systemctl", "systemctl"},
		{"no path of its own", "linux", "self", "agent-enter"},
	} {
		d, _ := scopeTestDeps(t)
		d.goos = tc.goos
		if tc.missing == "self" {
			d.self = ""
		}
		d.look = func(b string) (string, error) {
			if b == tc.missing {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + b, nil
		}
		if sc, why := holderScopes("demo", d); sc != nil || !strings.Contains(why, tc.why) {
			t.Errorf("%s: %v %q", tc.name, sc, why)
		}
	}
}

// Another build's holder: its socket answering is enough (a supervised
// holder has no unit); else its unit; a manager that cannot say is an error.
func TestHolderScopesActive(t *testing.T) {
	d, m := scopeTestDeps(t)
	m.active["unit.0123456789"] = true
	sc, _ := holderScopes("demo", d)
	for key, want := range map[string]bool{"answers.0123456789": true, "unit.0123456789": true, "gone.0123456789": false} {
		if got, err := sc.Active(key); err != nil || got != want {
			t.Errorf("%s: %v %v", key, got, err)
		}
	}
	if slices.Contains(m.asked, "demo/answers.0123456789") {
		t.Error("the manager asked for a holder whose socket answers")
	}
	m.err = errors.New("Failed to connect to bus")
	if _, err := sc.Active("gone.0123456789"); err == nil {
		t.Error("a manager that cannot say: no error")
	}
	d.manager = func() (service.HolderManager, error) { return nil, errors.New("systemd --user is not available") }
	sc, _ = holderScopes("demo", d)
	if _, err := sc.Active("gone.0123456789"); err == nil {
		t.Error("no manager: no error")
	}
}
