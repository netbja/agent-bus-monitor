package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestShutdownBlockers(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fresh := now.Add(-1 * time.Minute).UnixMilli()
	stale := now.Add(-30 * time.Minute).UnixMilli()

	t.Run("clear when board done and peers idle", func(t *testing.T) {
		board := map[string]bus.BoardEntry{
			"task-20": {Owner: "coder", State: "done", Updated: 1},
		}
		agents := map[string]bus.AgentSnapshot{
			"master": {State: "working", TS: fresh}, // self: excluded
			"coder":  {State: "idle", TS: fresh},
		}
		if got := shutdownBlockers(board, agents, "master", now); len(got) != 0 {
			t.Fatalf("want no blockers, got %v", got)
		}
	})

	t.Run("empty board and idle peers is clear", func(t *testing.T) {
		agents := map[string]bus.AgentSnapshot{"coder": {State: "done", TS: fresh}}
		if got := shutdownBlockers(nil, agents, "master", now); len(got) != 0 {
			t.Fatalf("want no blockers, got %v", got)
		}
	})

	t.Run("non-done task blocks", func(t *testing.T) {
		board := map[string]bus.BoardEntry{
			"task-20": {Owner: "foureyes", State: "review", Updated: 1},
		}
		got := shutdownBlockers(board, nil, "master", now)
		if len(got) != 1 || !strings.Contains(got[0], "task-20") || !strings.Contains(got[0], "review") {
			t.Fatalf("want task blocker, got %v", got)
		}
	})

	t.Run("busy peer blocks, stale busy peer does not", func(t *testing.T) {
		agents := map[string]bus.AgentSnapshot{
			"coder":    {State: "working", TS: fresh},
			"oldcoder": {State: "working", TS: stale}, // crashed mid-work: dead, not busy
			"reviewer": {State: "blocked", TS: fresh},
		}
		got := shutdownBlockers(nil, agents, "master", now)
		if len(got) != 2 {
			t.Fatalf("want 2 blockers, got %v", got)
		}
		joined := strings.Join(got, " ")
		if !strings.Contains(joined, "coder") || !strings.Contains(joined, "reviewer") {
			t.Fatalf("missing busy peers: %v", got)
		}
		if strings.Contains(joined, "oldcoder") {
			t.Fatalf("stale peer should not block: %v", got)
		}
	})
}
