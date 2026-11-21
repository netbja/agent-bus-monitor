package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
)

// person-never-agent: `agentbus agents` lists no entry under the person's
// recorded name, whatever terminal it names; asked for by name, it is shown
// (the operator inspects it before agents forget).
func TestAgentsListLeavesThePersonOut(t *testing.T) {
	all := map[string]bus.AgentSnapshot{
		"master":  {State: "idle", Pane: "t2"},
		"charlie": {State: "waiting", Pane: "old-holder/t4"},
	}
	m, missing := listedAgents(all, nil, "charlie")
	if _, ok := m["charlie"]; ok || len(m) != 1 || len(missing) != 0 {
		t.Fatalf("all: %v missing %v", m, missing)
	}
	if _, ok := all["charlie"]; !ok {
		t.Fatal("the caller's map was changed")
	}
	if m, missing := listedAgents(all, []string{"charlie"}, "charlie"); len(m) != 1 || len(missing) != 0 {
		t.Fatalf("by name: %v missing %v", m, missing)
	} else if _, ok := m["charlie"]; !ok {
		t.Fatalf("by name, the person's entry not shown: %v", m)
	}
	if m, missing := listedAgents(all, []string{"charlie", "master"}, "charlie"); len(m) != 2 || len(missing) != 0 {
		t.Fatalf("two names: %v missing %v", m, missing)
	}
	if m, _ := listedAgents(all, nil, ""); len(m) != 2 {
		t.Fatalf("no person: %v", m)
	}
}

// foureyes on 4f42cfce: the built binary's `agents` goes through listedAgents
// with the person's identity.as, on a real broker: the person's entry is
// left out of the list and shown when named.
func TestAgentsCommandLeavesThePersonOut(t *testing.T) {
	b, _ := bustest.Open(t)
	ctx := context.Background()
	for name, pane := range map[string]string{"master": "t2", "charlie": "old-holder/t4"} {
		if _, err := b.Status(ctx, name, "idle", "", bus.AgentIdent{Pane: pane, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[identity]\nas = \"charlie\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := func(args ...string) map[string]bus.AgentSnapshot {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-p", b.Project(), "agents", "--json"}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "REDIS_URL=" + os.Getenv("REDIS_URL")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("agents %v: %v %s", args, err, out)
		}
		var m map[string]bus.AgentSnapshot
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("agents %v: %v %s", args, err, out)
		}
		return m
	}
	if m := list(); m["master"].Pane != "t2" || len(m) != 1 {
		t.Fatalf("the list: %v", m)
	}
	if m := list("charlie"); m["charlie"].Pane != "old-holder/t4" {
		t.Fatalf("named: %v", m)
	}
}
