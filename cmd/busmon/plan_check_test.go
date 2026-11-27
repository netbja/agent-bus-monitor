package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"
)

// onboarding §12.5: the task page shows the task's check in full, wrapped;
// a task without one shows no Check row.
func TestPlanTaskPageCheck(t *testing.T) {
	check := "go test ./plan -run 'TestParseCheck|TestCheckFieldsOmitted' -count=1 && go test ./cmd/busmon -run TestPlanTaskPageCheck -count=1 && scripts/check.sh"
	p := planState{rows: []plan.Row{
		{Task: plan.Task{ID: "checked", Title: "x", Owner: "coder", Check: check}, State: plan.Upcoming},
		{Task: plan.Task{ID: "plain", Title: "y", Owner: "coder"}, State: plan.Upcoming},
	}}
	_, body, copied := planTaskDetail(p, "checked", time.Now())
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.Contains(collapse(copied), "Check: "+check) {
		t.Fatalf("the copy has no full check:\n%s", copied)
	}
	if len([]rune(check)) <= mdWidth {
		t.Fatal("the fixture's check fits one row: it tests no wrap")
	}
	var drawn []string
	for _, l := range body {
		if w := l.Width(); w > mdWidth {
			t.Errorf("a row of %d cells past %d: %q", w, mdWidth, l.text())
		}
		drawn = append(drawn, strings.TrimSpace(l.text()))
	}
	if joined := collapse(strings.Join(drawn, " ")); !strings.Contains(joined, "Check: "+check) {
		t.Fatalf("the page cuts the check:\n%s", strings.Join(drawn, "\n"))
	}
	_, body, _ = planTaskDetail(p, "plain", time.Now())
	if strings.Contains(plainOf(body), "Check:") {
		t.Fatalf("a task without a check shows one:\n%s", plainOf(body))
	}
}
