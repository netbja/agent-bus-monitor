package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type pluginManifest struct {
	ID      string `toml:"id"`
	Version string `toml:"version"`
	Actions []struct {
		ID      string   `toml:"id"`
		Command []string `toml:"command"`
	} `toml:"actions"`
	Panes []struct {
		ID      string   `toml:"id"`
		Command []string `toml:"command"`
	} `toml:"panes"`
	Events []struct {
		On      string   `toml:"on"`
		Command []string `toml:"command"`
	} `toml:"events"`
}

func TestPluginManifest(t *testing.T) {
	const path = "../../herdr-plugin.toml"
	var m pluginManifest
	md, err := toml.DecodeFile(path, &m)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if m.ID != "netbja.agentbus" {
		t.Errorf("id = %q", m.ID)
	}
	allowed := map[string]bool{"./agentbus": true, "./busmon": true}
	seen := map[string]bool{}
	for _, a := range m.Actions {
		if seen[a.ID] {
			t.Errorf("duplicate action id %q", a.ID)
		}
		seen[a.ID] = true
		if len(a.Command) == 0 || !allowed[a.Command[0]] {
			t.Errorf("action %q command %v", a.ID, a.Command)
		}
	}
	seen = map[string]bool{}
	for _, p := range m.Panes {
		if seen[p.ID] {
			t.Errorf("duplicate pane id %q", p.ID)
		}
		seen[p.ID] = true
		if len(p.Command) == 0 || !allowed[p.Command[0]] {
			t.Errorf("pane %q command %v", p.ID, p.Command)
		}
	}
	// v0.21: the only events are the workspace hook, and they run only it.
	hook := map[string]bool{"workspace.created": true, "workspace.focused": true}
	for _, e := range m.Events {
		if !hook[e.On] || strings.Join(e.Command, " ") != "./agentbus plugin workspace-event" {
			t.Errorf("event %q command %v", e.On, e.Command)
		}
		delete(hook, e.On)
	}
	if len(hook) != 0 {
		t.Errorf("missing workspace events %v", hook)
	}
	for _, k := range []string{"build"} {
		if md.IsDefined(k) {
			t.Errorf("manifest must not define %q", k)
		}
	}
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "--match", "v*").Output()
	if err != nil {
		t.Skipf("no git or no v* tag: %v", err)
	}
	tag := strings.TrimSpace(string(out))
	if !versionAtLeast(m.Version, tag) {
		t.Errorf("manifest version %q is older than the newest tag %s", m.Version, tag)
	}
}
