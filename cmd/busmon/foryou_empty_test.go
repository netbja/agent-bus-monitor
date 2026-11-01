package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/plan"
)

// When nothing is expected from the person, For you says so, and what is in
// progress and the next decision to come (the person: "I must always know
// what is expected"), from what busmon already reads: agents and the plan.
func TestForYouEmptyState(t *testing.T) {
	s := snapshot{agents: map[string]*agentState{
		"coder":    {state: "working", message: "pages step 4: recap"},
		"foureyes": {state: "idle", message: "reviewed"},
	}}
	s.planState.loaded = true
	s.planState.rows = []plan.Row{
		{Task: plan.Task{ID: "pages-4", Title: "Recap and For you", Owner: "coder"}, State: plan.InProgress},
		{Task: plan.Task{ID: "notifications-order", Title: "Decide: notifications order", Owner: plan.Person}, State: plan.Upcoming},
	}
	text := plainOf(forYouEmpty(s))
	for _, w := range []string{"Nothing expected from you.", "In progress: coder " + GlyphNone + " pages step 4: recap", "Next for you: Decide: notifications order"} {
		if !strings.Contains(text, w) {
			t.Errorf("empty state lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, "foureyes") {
		t.Errorf("an idle agent listed as in progress:\n%s", text)
	}
	// Nothing in progress, no plan: it still says what it knows.
	text = plainOf(forYouEmpty(snapshot{}))
	if !strings.Contains(text, "Nothing expected from you.") || !strings.Contains(text, "In progress: nobody declares working") || !strings.Contains(text, "Next for you: no decision planned") {
		t.Errorf("bare empty state:\n%s", text)
	}
}

// Through the shell: an empty For you shows the three lines whole (the
// Requests fixture's shell has nothing for the person).
func TestForYouPanelShowsTheEmptyState(t *testing.T) {
	m := reqShell(t, 120, 40, nil)
	if len(m.forYou) != 0 {
		t.Fatalf("fixture has %d for-you items", len(m.forYou))
	}
	screen := m.grid().plain()
	for _, w := range []string{"Nothing expected from you.", "In progress:", "Next for you:"} {
		if !strings.Contains(screen, w) {
			t.Errorf("For you lacks %q:\n%s", w, screen)
		}
	}
}

// The recap's For you section shows only when the For you panel is not on
// screen (the person's answer); shown and empty, it carries the empty state.
func TestRecapForYouOnlyWithoutThePanel(t *testing.T) {
	r := recapModel{Rows: []recapRow{
		{Section: recapForYou, Title: recapForYou},
		{Key: "ask:1:q", Section: recapForYou, Title: "master asks · go?", Selectable: true},
		{Section: "Agents", Title: "Agents"},
		{Key: "agent:coder", Section: "Agents", Title: "coder · working", Selectable: true},
	}}
	empty := forYouEmpty(snapshot{})
	text := func(m recapModel) string {
		var b strings.Builder
		for _, row := range m.Rows {
			b.WriteString(row.Section + "|" + row.Title + "\n")
		}
		return b.String()
	}
	if got := text(recapForScreen(r, true, empty)); strings.Contains(got, recapForYou) || !strings.Contains(got, "coder") {
		t.Errorf("panel on screen, the recap repeats For you:\n%s", got)
	}
	if got := text(recapForScreen(r, false, empty)); !strings.Contains(got, "master asks") || strings.Contains(got, "Nothing expected") {
		t.Errorf("panel absent, the recap lost For you:\n%s", got)
	}
	r.Rows = append(r.Rows[:1], r.Rows[2:]...) // nothing for the person
	got := text(recapForScreen(r, false, empty))
	if !strings.Contains(got, recapForYou+"|Nothing expected from you.") || strings.Index(got, "Nothing expected") > strings.Index(got, "Agents|") {
		t.Errorf("panel absent and nothing expected: the empty state should lead the section:\n%s", got)
	}
}
