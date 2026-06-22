package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestUsageTable(t *testing.T) {
	now := time.UnixMilli(1_700_000_100_000)
	m := map[string]bus.UsageSnapshot{
		"claude1": {Model: "Opus 4.8", Session: "99.0%", Reset: "36m", TS: now.Add(-10 * time.Second).UnixMilli()},
		"claude2": {Weekly: "41.0%", TS: now.Add(-5 * time.Minute).UnixMilli()},
	}
	out := usageTable(m, now)
	if !strings.Contains(out, "claude1") || !strings.Contains(out, "Opus 4.8") || !strings.Contains(out, "99.0%") || !strings.Contains(out, "36m") {
		t.Fatalf("missing claude1 usage: %q", out)
	}
	if !strings.Contains(out, "10s ago") {
		t.Fatalf("claude1 age wrong: %q", out)
	}
	if !strings.Contains(out, "claude2") || !strings.Contains(out, "41.0%") {
		t.Fatalf("missing claude2: %q", out)
	}
	if strings.Index(out, "claude1") > strings.Index(out, "claude2") {
		t.Fatalf("rows not sorted by name: %q", out)
	}
}
