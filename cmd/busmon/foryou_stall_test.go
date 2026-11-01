package main

import (
	"strings"
	"testing"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

// The stall guard's two For-you rows (stall guard step 3): a team waiting on
// the person's own task, and a hand-off held by the person's focused pane.
// Only while the record is a current warn with that reason; never otherwise.
func TestForYouListsTheStallGuardsPersonRows(t *testing.T) {
	now := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	rec := func(id, reason, detail string, observed time.Time) guardian.Record {
		return guardian.Record{ID: id, Status: guardian.Warn, Reason: reason, Detail: detail,
			ObservedAt: observed.UnixMilli(), Since: now.Add(-3 * time.Minute).UnixMilli()}
	}
	s := snapshot{agents: map[string]*agentState{}, guardianHealth: map[string]guardian.Record{
		"team-stall":  rec("team-stall", "waiting-person", "team idle 18m; waiting for you: Reinstall and tag v0.26", now),
		"clear-stall": rec("clear-stall", "pane-focused", "coder handed off 22m ago; not cleared: the pane is focused", now),
	}}
	s.guardianNow = now
	items := forYou(s, nil)
	var texts []string
	for _, it := range items {
		if it.Rank != rankHealth {
			t.Errorf("unexpected item %+v", it)
		}
		texts = append(texts, it.Agent+"|"+it.Text)
	}
	got := strings.Join(texts, "\n")
	for _, w := range []string{"stall|team idle 18m; waiting for you: Reinstall and tag v0.26", "clear|coder handed off 22m ago"} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	if len(decisions(items)) != 2 {
		t.Errorf("the rows are decisions for the badge: %d", len(decisions(items)))
	}
	// Not for the person: another reason, an ok, a stale record.
	s.guardianHealth["team-stall"] = rec("team-stall", "", "team idle 18m, nothing open; next ready: t1 (coder)", now)
	s.guardianHealth["clear-stall"] = rec("clear-stall", "pane-focused", "old", now.Add(-10*time.Minute)) // stale: two intervals
	if items := forYou(s, nil); len(items) != 0 {
		t.Errorf("rows that are not the person's: %+v", items)
	}
}
