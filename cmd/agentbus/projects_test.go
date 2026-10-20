package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

func projectsRig(t *testing.T) (projectsDeps, *fakeManager) {
	dir := t.TempDir()
	tpl := "name = \"demo\"\nworking_dir = \"/w/demo\"\n[[tabs]]\nname = \"busmon\"\ncommand = \"busmon --project demo\"\n" +
		"[[tabs]]\nname = \"master\"\ncommand = \"agent-launch master demo\"\n[[tabs]]\nname = \"coder\"\ncommand = \"agent-launch coder demo\"\n" +
		"[[tabs]]\nname = \"sentinel\"\ncommand = \"agent-launch sentinel demo\"\n"
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFakeManager()
	m.specs["demo/dispatch"] = service.Spec{}
	m.specs["demo/missions"] = service.Spec{}
	m.active["demo/dispatch"] = true
	m.specs["orphan/dispatch"] = service.Spec{Project: "orphan", Kind: "dispatch"}
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": dir}
	return projectsDeps{
		Env: func(k string) string { return env[k] },
		Keys: func(_ context.Context, p string) ([]string, error) {
			if p == "demo" {
				return []string{"demo:status"}, nil
			}
			return nil, nil
		},
		Agents: func(string) (map[string]bus.AgentSnapshot, error) {
			return map[string]bus.AgentSnapshot{"master": {PID: 1, PIDStart: 5}, "coder": {PID: 2, PIDStart: 5}}, nil
		},
		ProcStart: func(pid int) (uint64, error) {
			if pid == 1 {
				return 5, nil
			}
			return 0, os.ErrNotExist
		},
		Services: func() (service.Manager, string) { return m, "" },
	}, m
}

func TestProjectsListsTemplatesUnitsAndAgents(t *testing.T) {
	d, _ := projectsRig(t)
	var out, errw bytes.Buffer
	if code := runProjects(nil, d, &out, &errw); code != 0 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	s := out.String()
	for _, w := range []string{"demo", "present", "stopped", "1 alive / 1 dead / 1 ?", "/w/demo", "orphan", "absent", "(no template)", "agentbus up demo"} {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "agentbus up orphan") {
		t.Errorf("a project without template has no up hint:\n%s", s)
	}
}

func TestProjectsWithRedisDownSaysQuestionMarks(t *testing.T) {
	d, _ := projectsRig(t)
	d.Keys = func(context.Context, string) ([]string, error) { return nil, errors.New("connection refused") }
	var out, errw bytes.Buffer
	if code := runProjects([]string{"--json"}, d, &out, &errw); code != 0 {
		t.Fatalf("code %d", code)
	}
	var lines []projectLine
	if err := json.Unmarshal(out.Bytes(), &lines); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Broker != "?" || lines[0].Agents || lines[0].Services != "stopped" {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestProjectsWithNothingSaysHowToStart(t *testing.T) {
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": t.TempDir()}
	var out, errw bytes.Buffer
	if code := runProjects(nil, projectsDeps{Env: func(k string) string { return env[k] }}, &out, &errw); code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "agentbus up <project> <path>") {
		t.Fatalf("out = %s", out.String())
	}
	out.Reset()
	runProjects([]string{"--json"}, projectsDeps{Env: func(k string) string { return env[k] }}, &out, &errw)
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("json = %s", out.String())
	}
}

// projects must stay above the project-required exit in main().
func TestProjectsNeedsNoProject(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	at, exit := strings.Index(s, `args[0] == "projects"`), strings.Index(s, `die("project required`)
	if at < 0 || exit < 0 || at > exit {
		t.Fatalf("projects block at %d, project-required exit at %d", at, exit)
	}
}

// A project only the broker knows (made on another machine) is listed too.
func TestProjectsListsBrokerOnlyProjects(t *testing.T) {
	d, _ := projectsRig(t)
	d.Broker = func(context.Context) ([]string, error) { return []string{"remote", "demo"}, nil }
	base := d.Keys
	d.Keys = func(ctx context.Context, p string) ([]string, error) {
		if p == "remote" {
			return []string{"remote:status"}, nil
		}
		return base(ctx, p)
	}
	var out, errw bytes.Buffer
	if code := runProjects([]string{"--json"}, d, &out, &errw); code != 0 {
		t.Fatalf("code %d", code)
	}
	var lines []projectLine
	if err := json.Unmarshal(out.Bytes(), &lines); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range lines {
		names = append(names, l.Name+":"+l.Broker)
	}
	if strings.Join(names, ",") != "demo:present,orphan:absent,remote:present" {
		t.Fatalf("rows = %q", names)
	}
}
