package relaunch

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The service entry talks only to the session its env file names; with none
// it refuses rather than guess (self-repair spec 4.1).
func TestServiceHerdrRefusesWithoutASession(t *testing.T) {
	if _, err := ServiceHerdr(envOf(nil)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("no session: %v", err)
	}
	for _, k := range []string{"HERDR_SESSION", "HERDR_SOCKET_PATH"} {
		if h, err := ServiceHerdr(envOf(map[string]string{k: "x"})); err != nil || h == nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
}

// It runs the binary the env file names (HERDR_BIN_PATH first, then HERDR_BIN).
func TestServiceHerdrRunsTheNamedBinary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, out string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+out+" \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	path, bin := write("by-path", "path"), write("by-bin", "bin")
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"HERDR_SESSION": "s", "HERDR_BIN_PATH": path, "HERDR_BIN": bin}, "path workspace list"},
		{map[string]string{"HERDR_SESSION": "s", "HERDR_BIN": bin}, "bin workspace list"},
	} {
		h, err := ServiceHerdr(envOf(c.env))
		if err != nil {
			t.Fatal(err)
		}
		out, err := h("workspace", "list")
		if err != nil || strings.TrimSpace(string(out)) != c.want {
			t.Fatalf("%v: %q %v, want %q", c.env, out, err, c.want)
		}
	}
}

// The service entry takes up's lock without waiting, never runs Role without
// it, and releases it.
func TestServiceRoleTakesTheProjectLock(t *testing.T) {
	ran := false
	d := Deps{Herdr: func(args ...string) ([]byte, error) { ran = true; return nil, errors.New("no herdr here") }}
	if err := ServiceRole(d, nil, "demo", "coder", "", Template{}, io.Discard); err == nil || ran {
		t.Fatalf("no lock: %v, ran %v", err, ran)
	}
	busy := func(string, bool) (func(), error) { return nil, ErrBusy }
	if err := ServiceRole(d, busy, "demo", "coder", "", Template{}, io.Discard); !errors.Is(err, ErrBusy) || ran {
		t.Fatalf("busy: %v, ran %v", err, ran)
	}
	var waited, unlocked bool
	lock := func(_ string, wait bool) (func(), error) { waited = wait; return func() { unlocked = true }, nil }
	tpl := Template{Tabs: []Tab{{Name: "coder", Command: "agent-launch coder demo"}}}
	err := ServiceRole(d, lock, "demo", "coder", "", tpl, io.Discard)
	if err == nil || !ran || waited || !unlocked {
		t.Fatalf("held: err %v ran %v waited %v unlocked %v", err, ran, waited, unlocked)
	}
}

// Role keeps the caller's template whole: it narrows a copy to the role's tab.
func TestRoleKeepsTheCallersTemplate(t *testing.T) {
	tpl := Template{Tabs: []Tab{{Name: "master", Command: "agent-launch master demo"}, {Name: "coder", Command: "agent-launch coder demo"}}}
	d := Deps{Herdr: func(args ...string) ([]byte, error) { return nil, errors.New("stop here") }}
	_ = Role(d, "demo", "coder", "", tpl, io.Discard)
	if len(tpl.Tabs) != 2 || tpl.Tabs[0].Name != "master" || tpl.Tabs[1].Name != "coder" {
		t.Fatalf("template changed: %+v", tpl.Tabs)
	}
}

func TestFindWorkspace(t *testing.T) {
	list := `{"result":{"workspaces":[{"label":"demo","workspace_id":"w1"},{"label":"demo","workspace_id":"w2"},{"label":"other","workspace_id":"w3"}]}}`
	h := func(args ...string) ([]byte, error) { return []byte(list), nil }
	for _, c := range []struct{ project, ignore, want string }{{"demo", "", "w1"}, {"demo", "w1", "w2"}, {"none", "", ""}} {
		if got, err := FindWorkspace(h, c.project, c.ignore); err != nil || got != c.want {
			t.Fatalf("%+v: %q %v", c, got, err)
		}
	}
	if _, err := FindWorkspace(func(...string) ([]byte, error) { return []byte("{"), nil }, "demo", ""); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestReadTemplate(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "agentbus", "projects")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("name = \"demo\"\nworking_dir = \"~/src\"\n[[tabs]]\nname = \"coder\"\ncommand = \"agent-launch coder demo\"\n"), 0o644)
	env := envOf(map[string]string{"XDG_CONFIG_HOME": cfg})
	tpl, err := ReadTemplate(env, "demo")
	if err != nil || tpl.WorkingDir != "~/src" || len(tpl.Tabs) != 1 || tpl.Tabs[0].Name != "coder" {
		t.Fatalf("%+v %v", tpl, err)
	}
	if _, err := ReadTemplate(env, "other"); err == nil {
		t.Fatal("a missing template read")
	}
}

func TestCacheDir(t *testing.T) {
	if got := CacheDir(envOf(map[string]string{"XDG_CACHE_HOME": "/x", "HOME": "/h"})); got != "/x/agentbus" {
		t.Fatal(got)
	}
	if got := CacheDir(envOf(map[string]string{"HOME": "/h"})); got != "/h/.cache/agentbus" {
		t.Fatal(got)
	}
}
