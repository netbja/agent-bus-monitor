package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

func driftManifest(t *testing.T) *rolecfg.Manifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roles.toml")
	toml := `schema_version = 2
[profiles.p]
client = "codex"
provider = "openai"
model = "test"
[roles.master]
profile = "p"
tier = "boot"
required = true
[roles.coder]
profile = "p"
tier = "boot"
required = true
[roles.foureyes]
profile = "p"
tier = "boot"
[roles.architect]
profile = "p"
tier = "pop"
[roles.docs]
profile = "p"
tier = "pop"
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// board-drift §5: each kind of finding, one line each, in a fixed order.
func TestBoardDriftFindings(t *testing.T) {
	in := driftInput{
		Project: "demo",
		Merged: []dispatchsvc.DriftMerged{
			{Task: "step-a", Owner: "coder", State: "accepted", Branch: "feat/a", Head: "0123456789abcdef0123456789abcdef01234567", Base: "dev"},
			{Task: "step-b", Owner: "coder", State: "review", Branch: "", Unjudged: true},
		},
		Board: map[string]bus.BoardEntry{
			"lost":      {Owner: "docs", State: "requested", Request: &bus.RequestInfo{From: "master", Target: "docs", Availability: "missing"}},
			"lost-done": {Owner: "docs", State: "done", Request: &bus.RequestInfo{From: "master", Target: "docs", Availability: "missing"}},
			"kept":      {Owner: "docs", State: "accepted", Request: &bus.RequestInfo{From: "master", Target: "docs", Availability: "retained"}},
			"spec":      {Owner: "architect", State: "review", Request: &bus.RequestInfo{From: "master", Target: "architect", Availability: "retained"}},
			"audit":     {Owner: "sec", State: "requested", Request: &bus.RequestInfo{From: "master", Target: "sec", Availability: "retained"}},
		},
		Agents: map[string]bus.AgentSnapshot{
			"master":    {State: "idle", Pane: "t1"},
			"coder":     {State: "working", Pane: "t2"},
			"foureyes":  {State: "idle", Pane: "t3"},
			"architect": {State: "idle", Pane: "t4"},              // a pop by roles.toml only
			"sec":       {State: "idle", Pane: "t6", Tier: "pop"}, // a pop by its snapshot only
			"docs":      {State: "idle", Pane: "t5", Tier: "pop"},
		},
		Live:     map[string]bool{"master": true},
		Manifest: driftManifest(t),
		Resume:   map[string]bus.ResumeRecord{"docs": {Session: "c2bf3f3b-0000-4000-8000-000000000000"}, "architect": {Session: "not a session"}},
	}
	got := boardDrift(in)
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Text)
	}
	want := []string{
		"merged: step-a (accepted, coder): branch feat/a head 0123456 is in dev; its owner closes it (agentbus request done step-a \"…\") or, for a step, sends its review",
		"cannot be judged: step-b (review, coder): no branch tip and no recorded commit; record it: agentbus request review step-b --commit <sha>",
		"body lost: lost (requested, master → docs): its body is gone from the stream; docs or master closes it: agentbus request done lost \"<why>\"",
		"no terminal: coder is required and has no terminal",
		"no terminal: architect has no terminal and owns open requests: spec (review). Bring it back: agentbus --project demo pop architect",
		"no terminal: docs has no terminal and owns open requests: kept (accepted), lost (requested). Bring it back: agentbus --project demo pop docs --resume c2bf3f3b-0000-4000-8000-000000000000",
		"no terminal: sec has no terminal and owns open requests: audit (requested). Bring it back: agentbus --project demo pop sec",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	// foureyes has no terminal but is neither required nor a pop: not drift.
}

// What could not be read is said, never taken as no drift.
func TestBoardDriftNotJudged(t *testing.T) {
	in := driftInput{
		Project:   "demo",
		MergedWhy: "no git repository here",
		Board:     map[string]bus.BoardEntry{},
		Agents:    map[string]bus.AgentSnapshot{"coder": {State: "idle", Pane: "t2"}},
	}
	var lines []string
	for _, f := range boardDrift(in) {
		lines = append(lines, f.Text)
	}
	want := []string{
		"not judged: merged branches: no git repository here",
		"not judged: terminals: the project's holder was not read",
		"not judged: required roles: no roles.toml read",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// No drift: one line and exit 0; drift: the lines and exit 1; --json the
// findings.
func TestBoardDriftPrint(t *testing.T) {
	var out strings.Builder
	if code := printDrift(&out, nil, false); code != 0 || out.String() != "no drift\n" {
		t.Fatalf("none: %d %q", code, out.String())
	}
	out.Reset()
	fs := []driftFinding{{Kind: "body-lost", Task: "t", Text: "body lost: t"}}
	if code := printDrift(&out, fs, false); code != 1 || out.String() != "body lost: t\n" {
		t.Fatalf("drift: %d %q", code, out.String())
	}
	out.Reset()
	if code := printDrift(&out, fs, true); code != 1 {
		t.Fatalf("json: %d", code)
	}
	var back []driftFinding
	if err := json.Unmarshal([]byte(out.String()), &back); err != nil || len(back) != 1 || back[0] != fs[0] {
		t.Fatalf("json: %v %q", err, out.String())
	}
	out.Reset()
	if code := printDrift(&out, nil, true); code != 0 || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("json none: %d %q", code, out.String())
	}
}
