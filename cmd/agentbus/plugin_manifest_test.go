package main

import (
	"bytes"
	"errors"
	"os"
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
	Build []struct {
		Command []string `toml:"command"`
	} `toml:"build"`
}

func TestPluginManifest(t *testing.T) {
	const path = "../../herdr-plugin.toml"
	var m pluginManifest
	_, err := toml.DecodeFile(path, &m)
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
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "--match", "v*").Output()
	if err != nil {
		t.Skipf("no git or no v* tag: %v", err)
	}
	tag := strings.TrimSpace(string(out))
	if !versionAtLeast(m.Version, tag) {
		t.Errorf("manifest version %q is older than the newest tag %s", m.Version, tag)
	}
}

// plugin-listing: herdr's plugin list offers `herdr plugin install`, which clones and runs only
// [[build]]. agent-bus is set up by ./setup.sh (broker, binaries, skills) and linked, never
// installed (decision 2026-10-19-herdr-plugin), so its one build step refuses: it exits 1, herdr
// aborts the install and registers nothing, and the message says how to install it. plugin link
// never runs [[build]], so setup's link is unaffected.
func TestPluginBuildRefusesInstall(t *testing.T) {
	var m pluginManifest
	if _, err := toml.DecodeFile("../../herdr-plugin.toml", &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Build) != 1 {
		t.Fatalf("want exactly one [[build]] step (the refusal), got %d", len(m.Build))
	}
	argv := m.Build[0].Command
	if len(argv) == 0 {
		t.Fatal("empty build command")
	}
	dir := t.TempDir()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("build step must exit 1 so herdr aborts the install, got %v", err)
	}
	msg := stderr.String()
	for _, want := range []string{
		"git clone https://github.com/netbja/agent-bus-monitor",
		"./setup.sh install",
		"herdr plugin install",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message lacks %q:\n%s", want, msg)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("refusal writes to stdout: %q", stdout.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("refusal wrote %d file(s) into the install directory", len(ents))
	}
}
