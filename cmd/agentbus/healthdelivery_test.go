package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A9: a consumer group whose agent is not in {p}:agents (a dead name like
// abm-claude, never forgotten) is ignored by cmd-delivery: its lag over 1000
// tripped "fail" for nobody. Registered groups still count, and the detail
// says how many were ignored.
func TestDeliveryEvidenceIgnoresUnregisteredGroups(t *testing.T) {
	now := time.UnixMilli(10_000_000)
	groups := []bus.CmdDeliveryGroup{
		{Group: "abm-claude", Lag: 1500, Pending: 3, OldestAt: now.Add(-48 * time.Hour).UnixMilli()},
		{Group: "coder", Lag: 2, Pending: 1, OldestAt: now.Add(-time.Minute).UnixMilli()},
		{Group: "master", Lag: 0},
	}
	reg := map[string]bus.AgentSnapshot{"coder": {}, "master": {}}
	ev, ignored := deliveryEvidence(groups, reg, now)
	if ignored != 1 {
		t.Fatalf("ignored %d, want 1", ignored)
	}
	if !ev.Known || ev.Lag != 2 || !ev.HasPending || ev.Pending != time.Minute {
		t.Fatalf("evidence %+v", ev)
	}
	// Nothing registered: nothing judged, and still known.
	ev, ignored = deliveryEvidence(groups, map[string]bus.AgentSnapshot{}, now)
	if ignored != 3 || !ev.Known || ev.Lag != 0 || ev.HasPending {
		t.Fatalf("all unregistered: %+v ignored %d", ev, ignored)
	}
}
