package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planWorld is a herdr session with the demo workspace open (one coder tab,
// pane w4:p2) and a checkout holding scripts/agent-spawn, a stub that records
// its arguments and environment.
func planWorld(t *testing.T, env map[string]string) (upDeps, string) {
	t.Helper()
	dir := t.TempDir()
	tpl := "name = \"demo\"\nworking_dir = \"" + dir + "\"\n\n[[tabs]]\nname = \"coder\"\ncommand = \"/r/scripts/agent-launch coder demo\"\n"
	if env["TWO_CODER_TABS"] != "" { // a template with a second tab launching coder
		tpl += "\n[[tabs]]\nname = \"coder-2\"\ncommand = \"/r/scripts/agent-launch coder demo\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "spawned")
	stub := "#!/bin/sh\necho \"$* REQUIRE_BIND=$AGENT_LAUNCH_REQUIRE_BIND WAIT=$AGENT_SPAWN_WAIT\" > " + record + "\n"
	for _, s := range []string{"agent-spawn", "bootstrap"} {
		if err := os.WriteFile(filepath.Join(repo, "scripts", s), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	all := map[string]string{"HERDR_ENV": "1", "AGENTBUS_PROJECTS_DIR": dir, "AGENTBUS_REPO": repo, "HERDR_PANE_ID": "w4:p2"}
	for k, v := range env {
		all[k] = v
	}
	h := &settlingHerdr{relaunchHerdr: relaunchHerdr{tabs: `{"label":"coder","tab_id":"w4:t2"}`}}
	return upDeps{Herdr: h.run, Env: func(k string) string { return all[k] }}, record
}

func TestPlanRelaunchChecksEverythingBeforeTheKill(t *testing.T) {
	for name, c := range map[string]struct {
		role string
		pop  bool
		env  map[string]string
		want string // "" = ok
		wPop bool
	}{
		"a boot role with its tab":     {"coder", false, nil, "", false},
		"outside herdr":                {"coder", false, map[string]string{"HERDR_ENV": ""}, "not inside herdr", false},
		"no template":                  {"coder", false, map[string]string{"AGENTBUS_PROJECTS_DIR": "/nowhere"}, "template", false},
		"a pop from the workspace":     {"reviewer-codex", true, nil, "", true},
		"a pop from another workspace": {"reviewer-codex", true, map[string]string{"HERDR_PANE_ID": "w7:p1"}, "run reload from a pane of the demo workspace", false},
		"a pop with no agent-spawn":    {"reviewer-codex", true, map[string]string{"AGENTBUS_REPO": "/nowhere"}, "checkout", false},
		"a boot role with no tab":      {"sentinel", false, nil, "no tab launching sentinel and it is not a pop role", false},
		"two tabs launching the role":  {"coder", false, map[string]string{"TWO_CODER_TABS": "1"}, "the template has 2 tabs launching coder, want 1", false},
	} {
		t.Run(name, func(t *testing.T) {
			d, _ := planWorld(t, c.env)
			plan, err := planRelaunch(d, "demo", c.role, c.pop)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("err %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err %v, want %q", err, c.want)
			case err == nil && plan.Pop != c.wPop:
				t.Fatalf("plan %+v", plan)
			}
		})
	}
}

// A workspace that is not open is refused before the kill, whatever the role.
func TestPlanRelaunchNeedsTheWorkspaceOpen(t *testing.T) {
	d, _ := planWorld(t, nil)
	d.Herdr = func(args ...string) ([]byte, error) { return []byte(`{"result":{"workspaces":[]}}`), nil }
	if _, err := planRelaunch(d, "demo", "coder", false); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("err %v", err)
	}
}

// A pop comes back the way it was started: agent-spawn, its session resumed,
// the bind required and no wait (reload proves the agent itself).
func TestSpawnPopResumesThroughAgentSpawn(t *testing.T) {
	d, record := planWorld(t, nil)
	var out bytes.Buffer
	locked := 0
	d.Lock = func(project string, wait bool) (func(), error) {
		if project != "demo" || wait {
			t.Fatalf("lock(%q, %v)", project, wait)
		}
		locked++
		return func() { locked-- }, nil
	}
	if err := spawnPop(context.Background(), d, "demo", "reviewer-codex", "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := "reviewer-codex demo --resume 93ff25bd-34d8-4077-bf6c-65e6cb135bbf REQUIRE_BIND=1 WAIT=0\n"; string(got) != want {
		t.Fatalf("agent-spawn got %q, want %q", got, want)
	}
	if locked != 0 {
		t.Fatalf("up's lock left held: %d", locked)
	}
	// Under up's lock, so it never opens a tab while an up does; a held lock
	// spawns nothing.
	d.Lock = func(string, bool) (func(), error) { return nil, errors.New("another up holds the lock") }
	_ = os.Remove(record)
	if err := spawnPop(context.Background(), d, "demo", "reviewer-codex", "", &out); err == nil {
		t.Fatal("spawned while up's lock was held")
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("agent-spawn ran while up's lock was held")
	}
	d.Lock = nil
	if err := spawnPop(context.Background(), d, "demo", "reviewer-codex", "", &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(record); string(got) != "reviewer-codex demo REQUIRE_BIND=1 WAIT=0\n" {
		t.Fatalf("fresh agent-spawn got %q", got)
	}
}
