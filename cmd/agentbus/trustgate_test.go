package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gateFixture: a template with busmon, two Claude agents, a Codex agent and
// the dispatch tab; claude's trust is recorded after `after` polls (never when
// negative), codex is trusted.
func gateFixture(t *testing.T, after int) (string, *recHost, upDeps, *int) {
	t.Helper()
	work, templates, h, _, deps := upFixture(t)
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"demo\"\nworking_dir = \"" + work + "\"\n" +
		"[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo\"\n" +
		"[[tabs]]\nname = \"master\"\ncommand = \"" + agentTab(templates, "master") + "\"\n" +
		"[[tabs]]\nname = \"coder\"\ncommand = \"" + agentTab(templates, "coder") + "\"\n" +
		"[[tabs]]\nname = \"foureyes\"\ncommand = \"" + agentTab(templates, "foureyes") + "\"\n" +
		"[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n"
	if err := os.WriteFile(filepath.Join(templates, "demo.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	deps.Bootstrap = func([]string) error { return nil }
	polls := 0
	deps.TabClient = func(cmd string) string {
		switch {
		case strings.Contains(cmd, "foureyes"):
			return "codex"
		case strings.Contains(cmd, "agent-launch"):
			return "claude-code"
		}
		return ""
	}
	deps.Trusted = func(client, dir string) bool {
		if dir != work {
			t.Errorf("trust read for %q, want the project directory", dir)
		}
		return client == "codex" || after >= 0 && polls >= after
	}
	deps.Sleep = func(time.Duration) { polls++ }
	deps.TrustTimeout, deps.TrustPoll = 10*time.Second, 2*time.Second
	return work, h, deps, &polls
}

func TestUpGatesAgentsOnTrust(t *testing.T) {
	work, h, deps, polls := gateFixture(t, 2)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	coder := startIndex(h, "coder")
	for _, first := range []string{"master", "foureyes"} {
		if i := startIndex(h, first); i < 0 || i > coder {
			t.Fatalf("%s started at %d, coder at %d: %v", first, i, coder, h.started())
		}
	}
	if coder < 0 || *polls < 2 {
		t.Fatalf("coder at %d after %d polls", coder, *polls)
	}
	msg := out.String()
	// A10: the message says where to answer: the agent's own terminal.
	if !strings.Contains(msg, "answer it in master's terminal (agentbus attach master)") || !strings.Contains(msg, "agentbus up demo is locked") {
		t.Fatalf("out = %q", msg)
	}
}

func TestUpTrustTimeoutStartsNothingMore(t *testing.T) {
	work, h, deps, _ := gateFixture(t, -1)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
		t.Fatal("up succeeded without trust")
	}
	if startIndex(h, "coder") >= 0 {
		t.Fatal("coder started without trust")
	}
	if startIndex(h, "master") < 0 {
		t.Fatal("master, the gate's agent, was not started")
	}
	if !strings.Contains(errw.String(), "agentbus up demo") {
		t.Fatalf("no way forward: %q", errw.String())
	}
}

func TestUpWithoutAGateStartsEverything(t *testing.T) {
	work, h, deps, polls := gateFixture(t, 0) // trusted from the start
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if *polls != 0 || startIndex(h, "coder") < 0 || strings.Contains(out.String(), "trust") {
		t.Fatalf("polls %d out %q", *polls, out.String())
	}
}

func TestUpDryRunShowsTheGate(t *testing.T) {
	work, _, deps, _ := gateFixture(t, -1)
	var out, errw bytes.Buffer
	if code := upDryRun(deps, "demo", false, &out, &errw); code != 0 {
		t.Fatalf("dry run = %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "trust: Claude Code not yet accepted for "+work+"; master first, the others after") {
		t.Fatalf("out = %q", out.String())
	}
}

// foureyes: a client whose CLI is confirmed absent opens no trust gate. Its
// tabs start (master's policy), and nobody waits ten minutes for a dialog its
// missing CLI can never show.
func TestUpOpensNoTrustGateForAnAbsentCLI(t *testing.T) {
	work, h, deps, polls := gateFixture(t, -1) // claude's trust never comes
	deps.TabExe = func(cmd string) string {
		switch {
		case strings.Contains(cmd, "foureyes"):
			return "codex"
		case strings.Contains(cmd, "agent-launch"):
			return "claude"
		}
		return ""
	}
	deps.LookPath = func(exe string) (string, error) {
		if exe == "codex" {
			return "/bin/codex", nil
		}
		return "", errCLIAbsent
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if *polls != 0 || strings.Contains(out.String(), "asks whether you trust") {
		t.Fatalf("a trust gate waited for an absent CLI (%d polls): %q", *polls, out.String())
	}
	for _, role := range []string{"master", "coder", "foureyes"} {
		if startIndex(h, role) < 0 {
			t.Fatalf("%s not started: %v", role, h.started())
		}
	}
	if !strings.Contains(out.String(), "warning: master needs claude, not found") {
		t.Fatalf("no warning: %q", out.String())
	}
}
