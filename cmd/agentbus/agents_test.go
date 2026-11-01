package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestAgentsTable(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Message: "plan 10", TS: now.Add(-12 * time.Second).UnixMilli(), Pane: "w1:p1"},
		"helper":  {State: "done", TS: now.Add(-11 * time.Minute).UnixMilli()},
	}
	out := agentsTable(m, now)
	if !strings.Contains(out, "claude1") || !strings.Contains(out, "working") || !strings.Contains(out, "plan 10") {
		t.Fatalf("missing claude1 row: %q", out)
	}
	if !strings.Contains(out, "12s ago") {
		t.Fatalf("claude1 age wrong: %q", out)
	}
	if !strings.Contains(out, "bus activity stale") {
		t.Fatalf("helper (11m) should show stale bus activity: %q", out)
	}
	if strings.Index(out, "claude1") > strings.Index(out, "helper") {
		t.Fatalf("rows not sorted by name: %q", out)
	}
	if !strings.Contains(out, "⧉w1:p1") {
		t.Fatalf("claude1 pane badge missing: %q", out)
	}
}

func TestAgentPane(t *testing.T) {
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Pane: "w1:p1"},
		"claude2": {State: "idle"}, // no pane
	}
	if p, ok := agentPane(m, "claude1"); !ok || p != "w1:p1" {
		t.Fatalf("agentPane(claude1) = (%q,%v), want (w1:p1,true)", p, ok)
	}
	if _, ok := agentPane(m, "claude2"); ok {
		t.Fatal("agentPane(claude2) should be false (no pane)")
	}
	if _, ok := agentPane(m, "ghost"); ok {
		t.Fatal("agentPane(ghost) should be false (unknown)")
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s ago"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestAgentsTableShowsNeverForAnAgentWithoutAStatus(t *testing.T) {
	out := agentsTable(map[string]bus.AgentSnapshot{"coder": {Pane: "w2:p3"}}, time.Now())
	if !strings.Contains(out, "never") || strings.Contains(out, "h ago") || strings.Contains(out, "stale") {
		t.Fatalf("table = %q", out)
	}
}

func TestAgentsForgetRefusesLiveAgentsUnlessForced(t *testing.T) {
	now := time.Now()
	snaps := map[string]bus.AgentSnapshot{
		"gone":   {State: "working", TS: now.Add(-20 * 24 * time.Hour).UnixMilli()},
		"fresh":  {State: "idle", TS: now.Add(-time.Minute).UnixMilli()},
		"armed":  {State: "idle", TS: now.Add(-time.Hour).UnixMilli()},
		"booted": {Pane: "w2:p3"},
	}
	var forgot []string
	d := forgetDeps{
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) { return snaps, nil },
		Armed:  func(_ context.Context, a string) (bool, error) { return a == "armed", nil },
		Forget: func(_ context.Context, a string) (bool, error) {
			forgot = append(forgot, a)
			return a != "unknown", nil
		},
		Now: func() time.Time { return now },
	}
	var out, errw bytes.Buffer
	code := runAgentsForget(context.Background(), d, []string{"gone", "fresh", "armed", "booted", "unknown"}, &out, &errw)
	if code != 1 || strings.Join(forgot, ",") != "gone,booted,unknown" {
		t.Fatalf("code %d, forgot %v, %s", code, forgot, errw.String())
	}
	if !strings.Contains(out.String(), "gone: forgotten") || !strings.Contains(out.String(), "unknown: nothing to forget") ||
		!strings.Contains(errw.String(), "fresh: declared a status") || !strings.Contains(errw.String(), "armed: still listening") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}
	forgot = nil
	if code := runAgentsForget(context.Background(), d, []string{"fresh", "armed", "--force"}, &out, &errw); code != 0 || len(forgot) != 2 {
		t.Fatalf("--force: code %d, forgot %v", code, forgot)
	}
	if code := runAgentsForget(context.Background(), d, []string{"Bad Name"}, &out, &errw); code != 1 {
		t.Fatalf("invalid name: code %d", code)
	}
}

// stranger-test: a project no agent has joined yet says so, instead of printing nothing.
func TestAgentsTableSaysWhenNoAgentYet(t *testing.T) {
	out := agentsTable(map[string]bus.AgentSnapshot{}, time.Now())
	if !strings.Contains(out, "no agent has declared a status yet") || !strings.HasSuffix(out, "\n") {
		t.Fatalf("empty table: %q", out)
	}
	if strings.Contains(agentsTable(map[string]bus.AgentSnapshot{"coder": {State: "idle"}}, time.Now()), "no agent") {
		t.Fatal("the empty note shows with an agent present")
	}
}

// agents <name>... (agent-brief): only those agents, same columns; a name the
// hash does not hold is said, never silently dropped.
func TestSelectAgents(t *testing.T) {
	m := map[string]bus.AgentSnapshot{"coder": {State: "working"}, "master": {State: "idle"}, "foureyes": {State: "idle"}}
	got, missing := selectAgents(m, []string{"coder", "ghost", "master"})
	if len(got) != 2 || got["coder"].State != "working" || got["master"].State != "idle" || len(missing) != 1 || missing[0] != "ghost" {
		t.Fatalf("selectAgents = %v, missing %v", got, missing)
	}
	if all, missing := selectAgents(m, nil); len(all) != 3 || len(missing) != 0 {
		t.Fatalf("no names: %v %v", all, missing)
	}
}
