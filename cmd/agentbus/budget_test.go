package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestBudgetTable(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	m := map[string]bus.BudgetSnapshot{
		"anthropic": {
			Provider:     "anthropic",
			SessionPct:   25,
			SessionReset: "2026-07-23T11:30:00Z",
			WeeklyPct:    44,
			WeeklyReset:  "2026-07-28T13:00:00Z",
			Extra:        map[string]float64{"weekly_opus_pct": 12},
			TS:           now.Add(-2 * time.Minute).UnixMilli(),
		},
	}
	got := budgetTable(m, now)
	for _, want := range []string{
		"anthropic", "session 25%", "(resets in 1h30m)",
		"weekly 44%", "(resets in 5d3h)", "weekly_opus 12%", "2m ago",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("budgetTable missing %q in:\n%s", want, got)
		}
	}
}

// A window whose reset is in the past makes its percentage meaningless; saying
// so beats printing a negative countdown, which reads as a bug.
func TestBudgetTableRolledOverWindow(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	m := map[string]bus.BudgetSnapshot{
		"anthropic": {SessionPct: 99, SessionReset: "2026-07-23T09:00:00Z", TS: now.UnixMilli()},
	}
	got := budgetTable(m, now)
	if !strings.Contains(got, "window rolled over") {
		t.Errorf("want a rolled-over marker, got:\n%s", got)
	}
	if strings.Contains(got, "resets in -") {
		t.Errorf("negative countdown leaked into output:\n%s", got)
	}
}

func TestBudgetTableSortedAndEmpty(t *testing.T) {
	now := time.Now()
	m := map[string]bus.BudgetSnapshot{
		"openai":    {WeeklyPct: 1, TS: now.UnixMilli()},
		"anthropic": {WeeklyPct: 2, TS: now.UnixMilli()},
	}
	got := budgetTable(m, now)
	if strings.Index(got, "anthropic") > strings.Index(got, "openai") {
		t.Errorf("providers not sorted:\n%s", got)
	}
	if budgetTable(map[string]bus.BudgetSnapshot{}, now) != "" {
		t.Error("empty budget map should render nothing")
	}
}

// A missing source must produce a note, never a failure: refresh runs from the
// sentinel's cron wake, where a hard exit loses the whole caretaker pass.
func TestCollectBudgetsMissingSourceIsANote(t *testing.T) {
	got, notes := collectBudgets(filepath.Join(t.TempDir(), "absent.json"))
	if len(got) != 0 {
		t.Errorf("want no budgets, got %v", got)
	}
	if len(notes) == 0 {
		t.Error("a missing cache must produce a note")
	}
	if got, notes := collectBudgets(""); len(got) != 0 || len(notes) == 0 {
		t.Error("an empty path must produce a note, not a panic")
	}
}

func TestCollectBudgets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(p, []byte(`{"sessionUsage":25,"weeklyUsage":44}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, notes := collectBudgets(p)
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %v", notes)
	}
	if got["anthropic"].SessionPct != 25 {
		t.Errorf("got %+v", got["anthropic"])
	}
}
