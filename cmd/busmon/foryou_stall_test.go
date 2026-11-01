package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
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

// guardFake adds the health reads to the poll's fake, with a ServerTime that
// can fail.
type guardFake struct {
	*fakeReader
	recs    map[string]guardian.Record
	now     time.Time
	timeErr error
}

func (g *guardFake) Health(context.Context) (map[string]guardian.Record, error) { return g.recs, nil }
func (g *guardFake) HealthNotes(context.Context) (map[string]guardian.Note, error) {
	return map[string]guardian.Note{}, nil
}
func (g *guardFake) ServerTime(context.Context) (time.Time, error) { return g.now, g.timeErr }

// A failed server-time read must not freeze the age reference (reviewer-codex):
// the cached warn would stay current for ever, a For you row and a badge count
// long after its observation went stale. The reference advances with the local
// clock from the last good read.
func TestStallRowExpiresWhileServerTimeFails(t *testing.T) {
	server := time.Now().Add(-time.Hour) // a server clock offset from ours
	f := &guardFake{fakeReader: &fakeReader{calls: map[string]int{}}, now: server, recs: map[string]guardian.Record{
		"team-stall": {ID: "team-stall", Status: guardian.Warn, Reason: "waiting-person", Detail: "waiting for you",
			ObservedAt: server.UnixMilli(), Since: server.UnixMilli()},
	}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if items := forYou(takeSnapshot(st, time.Now()), nil); len(items) != 1 {
		t.Fatalf("a fresh warn is a row: %+v", items)
	}
	f.timeErr = errors.New("timeout")
	pollOnce(context.Background(), f, st, 2)
	later := time.Now().Add(2 * time.Hour) // well past two intervals
	if items := forYou(takeSnapshot(st, later), nil); len(items) != 0 {
		t.Errorf("a warn read only before the time reads failed stays a row: %+v", items)
	}
}
