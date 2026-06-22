package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestAgentsTable(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Message: "plan 10", TS: now.Add(-12 * time.Second).UnixMilli(), Pane: "w1:p1"},
		"hermes":  {State: "done", TS: now.Add(-11 * time.Minute).UnixMilli()},
	}
	out := agentsTable(m, now)
	if !strings.Contains(out, "claude1") || !strings.Contains(out, "working") || !strings.Contains(out, "plan 10") {
		t.Fatalf("missing claude1 row: %q", out)
	}
	if !strings.Contains(out, "12s ago") {
		t.Fatalf("claude1 age wrong: %q", out)
	}
	if !strings.Contains(out, "offline") {
		t.Fatalf("hermes (11m) should be offline: %q", out)
	}
	if strings.Index(out, "claude1") > strings.Index(out, "hermes") {
		t.Fatalf("rows not sorted by name: %q", out)
	}
	if !strings.Contains(out, "⧉w1:p1") {
		t.Fatalf("claude1 pane badge missing: %q", out)
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
