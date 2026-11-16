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
	return work, h, deps, &polls
}

// P1-9 (demo field test): up waited, holding the person's terminal, while
// master sat on Claude Code's trust dialog. up starts the gate agent (and the
// agents of trusted clients), says where to answer and how to go on, and
// returns at once without polling; a second up, once the trust is recorded,
// starts the others.
func TestUpGatesAgentsOnTrust(t *testing.T) {
	work, h, deps, polls := gateFixture(t, 2)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if *polls != 0 {
		t.Fatalf("up waited: %d polls", *polls)
	}
	for _, first := range []string{"master", "foureyes"} {
		if startIndex(h, first) < 0 {
			t.Fatalf("%s not started: %v", first, h.started())
		}
	}
	if startIndex(h, "coder") >= 0 {
		t.Fatal("coder started before the trust")
	}
	msg := out.String()
	// A10: the message says where to answer (the agent's own terminal, or
	// busmon), and how to go on.
	for _, want := range []string{"answer it in master's terminal (agentbus attach master) or in busmon's For you",
		"then run agentbus up demo to start the others (coder)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("out lacks %q: %q", want, msg)
		}
	}
	*polls = 2 // the person answered: the trust is recorded
	before := len(h.started())
	out.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("second up = %d: %s", code, errw.String())
	}
	if startIndex(h, "coder") < before || strings.Contains(out.String(), "trust") {
		t.Fatalf("the second up: started %v, out %q", h.started(), out.String())
	}
}

// Never answered: up has still returned, the gate's agent started, the rest
// not, and the way on said.
func TestUpReturnsWithoutTheTrust(t *testing.T) {
	work, h, deps, polls := gateFixture(t, -1)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if *polls != 0 || startIndex(h, "coder") >= 0 || startIndex(h, "master") < 0 {
		t.Fatalf("polls %d, started %v", *polls, h.started())
	}
	if !strings.Contains(out.String(), "agentbus up demo") {
		t.Fatalf("no way forward: %q", out.String())
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
