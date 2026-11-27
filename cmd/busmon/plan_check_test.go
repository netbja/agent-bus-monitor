package main

import (
	"encoding/json"
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

// A check the plan accepts but the page cannot draw as written (an emoji
// whose widths the terminal libraries disagree on loses its variation
// selector) says so on the page: the stored and the drawn check never
// differ silently (reviewer-codex on onboarding-a1).
func TestPlanTaskPageCheckDrawnAsWritten(t *testing.T) {
	seen := map[bool]bool{}
	for _, c := range []string{"go test ./plan", "go test ☁\ufe0f", "go test 👍🏽", "go test ℹ\ufe0f", "go test é e\u0301 中 ⚠", "go test \u202e kcehc", "go test \u2066x\u2069", "go\u200btest", "go test 👨\u200d👩\u200d👧"} {
		q, _ := json.Marshal(c)
		f, err := plan.Parse([]byte(`{"plan":"p","goal":"g","tasks":[{"id":"a","title":"x","owner":"coder","check":` + string(q) + `}]}`))
		if err != nil {
			continue // the plan refuses it: nothing to draw
		}
		p := planState{rows: []plan.Row{{Task: f.Tasks[0], State: plan.Upcoming}}}
		_, _, copied := planTaskDetail(p, "a", time.Now())
		note := strings.Contains(copied, checkAlteredNote)
		seen[note] = true
		if same := strings.Contains(copied, c); same == note {
			t.Errorf("%q: drawn as written %v, the note %v:\n%s", c, same, note, copied)
		}
	}
	if !seen[true] || !seen[false] {
		t.Fatalf("the fixture needs a check drawn as written and one changed: %v", seen)
	}
}
