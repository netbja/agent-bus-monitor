package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestBoardTable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := map[string]bus.BoardEntry{
		"task-20": {Owner: "foureyes", State: "review", Branch: "foureyes/task-20", Updated: now.Add(-3 * time.Minute).Unix()},
		"task-21": {Owner: "coder", State: "working", Branch: "coder/task-21", Updated: now.Add(-30 * time.Second).Unix()},
	}
	out := boardTable(m, now)
	for _, want := range []string{"TASK", "OWNER", "STATE", "BRANCH", "AGE",
		"task-20", "foureyes", "review", "foureyes/task-20", "3m ago", "task-21", "30s ago"} {
		if !strings.Contains(out, want) {
			t.Fatalf("board table missing %q:\n%s", want, out)
		}
	}
	// Newest activity first: task-21 (30s) sorts above task-20 (3m).
	if strings.Index(out, "task-21") > strings.Index(out, "task-20") {
		t.Fatalf("rows not sorted newest-first:\n%s", out)
	}
}

func TestBoardTableEmpty(t *testing.T) {
	if out := boardTable(map[string]bus.BoardEntry{}, time.Now()); !strings.Contains(out, "empty") {
		t.Fatalf("empty board should say so, got %q", out)
	}
}
