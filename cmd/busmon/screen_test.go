package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// draw renders a primitive onto a SimulationScreen at an exact size and returns
// what a terminal would show. Primitives are drawn directly rather than through
// a running Application: no event loop, no goroutine, no flake — and colour
// tags are resolved, so these assertions are against what the operator reads,
// not against the markup busmon emitted.
func draw(t *testing.T, p tview.Primitive, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("simulation screen: %v", err)
	}
	defer sim.Fini()
	sim.SetSize(w, h)
	p.SetRect(0, 0, w, h)
	p.Draw(sim)
	sim.Show()

	cells, cw, ch := sim.GetContents()
	var sb strings.Builder
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			if c := cells[y*cw+x]; len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			} else {
				sb.WriteRune(' ')
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// textPane is the widget busmon uses for every overlay, built the same way.
func textPane(content string) *tview.TextView {
	v := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	v.SetScrollable(true).SetBorder(true)
	v.SetText(content)
	return v
}

// noWiderThan reports the first line that overflows the terminal. A pane that
// draws past its own width corrupts whatever is beside it.
func noWiderThan(t *testing.T, screen string, w int) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimRight(screen, "\n"), "\n") {
		if n := len([]rune(line)); n > w {
			t.Errorf("row %d is %d cells wide, terminal is %d: %q", i, n, w, line)
		}
	}
}

// A long multi-line Unicode report has to survive the trip to the screen: the
// flat feed shows one clipped line, and this view is the only place the whole
// thing exists.
func TestScreenDetailShowsLongUnicodeBodyAndFitsNarrowTerminals(t *testing.T) {
	body := "résumé des étapes:\n" +
		strings.Repeat("  • une ligne assez longue pour déborder d'un terminal étroit — vraiment\n", 12) +
		"  • 日本語の行もある\n  • dernière ligne"
	e := bus.Event{
		ID: "1758141663000-0", Kind: "report", Agent: "coder", RKind: bus.ReportNote,
		Message: "résumé des étapes: • une ligne assez longue…", Full: body,
	}
	at := time.Date(2026, 9, 17, 22, 41, 3, 0, time.UTC)
	pane := textPane(detailText(e, at, at.Add(3*time.Minute), threadState{}))

	for _, size := range []struct{ w, h int }{{100, 30}, {40, 20}, {40, 8}} {
		screen := draw(t, pane, size.w, size.h)
		noWiderThan(t, screen, size.w)
		if !strings.Contains(screen, "coder") {
			t.Errorf("%dx%d: the author must be visible:\n%s", size.w, size.h, screen)
		}
	}
	// At a readable size the body itself is on screen, wrapped not truncated.
	screen := draw(t, pane, 100, 30)
	if !strings.Contains(screen, "résumé des étapes") {
		t.Errorf("the retained body must be shown:\n%s", screen)
	}
	if !strings.Contains(screen, "2026-09-17 22:41:03") {
		t.Errorf("the absolute date must be shown:\n%s", screen)
	}
}

// An entry published before Coordination v1 carries no completeness marker, and
// the screen has to admit that rather than imply the text is whole.
func TestScreenDetailAdmitsUnknownCompletenessOnOldEntries(t *testing.T) {
	e := bus.Event{ID: "1-0", Kind: "report", Agent: "hermes", RKind: bus.ReportNote,
		Message: "an old report from before the marker existed"}
	pane := textPane(detailText(e, time.Unix(1, 0), time.Unix(600, 0), threadState{}))
	screen := draw(t, pane, 110, 24)
	if !strings.Contains(screen, "completeness not marked") {
		t.Errorf("an unmarked entry must say its completeness is unverified:\n%s", screen)
	}
}

// Several requests at once, including one with a deadline and one without: the
// one without must not grow one on screen.
func TestScreenRequestsOverlayShowsEachRequestHonestly(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	reqs := []request{
		{Tracked: true, Task: "task-20", Target: "foureyes", State: "requested",
			Created: now.Add(-42 * time.Minute), Updated: now.Add(-42 * time.Minute),
			Delivery: "output_written", Expires: now.Add(18 * time.Minute)},
		{Tracked: true, Task: "task-21", Target: "coder", State: "accepted",
			Created: now.Add(-2 * time.Hour), Updated: now.Add(-20 * time.Minute),
			Accepted: now.Add(-20 * time.Minute), Delivery: "queued"},
		{Tracked: true, Task: "task-22", Target: "coder", State: "blocked",
			Created: now.Add(-3 * time.Hour), Updated: now.Add(-time.Hour),
			BlockedReason: "waiting on an API key", Delivery: "output_written"},
	}
	pane := textPane(requestsPanel(reqs, nil, now, 100))
	screen := draw(t, pane, 110, 30)

	for _, want := range []string{"task-20", "task-21", "task-22", "deadline in 18m",
		"no acceptance recorded", "waiting on an API key", "no deadline set"} {
		if !strings.Contains(screen, want) {
			t.Errorf("requests overlay missing %q:\n%s", want, screen)
		}
	}
	if strings.Count(screen, "deadline in") != 1 {
		t.Errorf("only the request that carries a deadline may show one:\n%s", screen)
	}
	noWiderThan(t, screen, 110)
}

// The narrow-terminal case for the follow-up view: still readable, still inside
// its frame, and still honest about the one thing it must never invent.
func TestScreenRequestsOverlayAtFortyColumns(t *testing.T) {
	now := time.Now()
	reqs := []request{{Tracked: true, Task: "a-very-long-task-slug-indeed", Target: "foureyes",
		State: "requested", Created: now.Add(-time.Hour), Updated: now.Add(-time.Hour), Delivery: "queued"}}
	pane := textPane(requestsPanel(reqs, nil, now, 36))
	screen := draw(t, pane, 40, 16)
	noWiderThan(t, screen, 40)
	if !strings.Contains(screen, "a-very-long-task") {
		t.Errorf("the task must still be identifiable at 40 columns:\n%s", screen)
	}
}

// A filter hides; it never forgets. Clearing it brings the whole journal back.
func TestFilterHidesWithoutForgetting(t *testing.T) {
	lines := []feedLine{
		{id: "1", ev: bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "rebase"}},
		{id: "2", ev: bus.Event{Kind: "report", Agent: "foureyes", Message: "review done"}},
		{id: "sep"}, // a day separator carries no event
	}
	agent := parseFilter("@coder")
	if !agent.match(lines[0]) || agent.match(lines[1]) || agent.match(lines[2]) {
		t.Error("@coder must keep the directive addressed to coder and drop the rest")
	}
	words := parseFilter("review")
	if words.match(lines[0]) || !words.match(lines[1]) {
		t.Error("a word filter must match the body")
	}
	empty := feedFilter{}
	for _, l := range lines {
		if !empty.match(l) {
			t.Error("an empty filter admits everything, separators included")
		}
	}
}

func TestFilterRecognisesThreadIDs(t *testing.T) {
	e := bus.Event{ID: "1758141663000-0", Kind: "cmd", Type: bus.CmdDirective, From: "a", Target: "b"}
	reply := bus.Event{ID: "1758141999000-0", Kind: "cmd", Type: bus.CmdReply, Ref: "1758141663000-0", From: "b", Target: "a"}
	f := parseFilter("1758141663000-0")
	if f.thread == "" {
		t.Fatalf("a stream id must parse as a thread filter: %+v", f)
	}
	if !f.match(feedLine{ev: e}) || !f.match(feedLine{ev: reply}) {
		t.Error("a thread filter must keep the root and its replies")
	}
	// A bare millisecond cursor, as pasted from `agentbus thread`, works too.
	if bare := parseFilter("1758141663000"); !bare.match(feedLine{ev: e}) {
		t.Errorf("a bare ms cursor must match its entry: %+v", bare)
	}
}

// The shared snapshot is written by the poller and the tail while the screen
// draws it; -race turns any slip here into a failure.
func TestSharedStateIsSafeUnderConcurrentAccess(t *testing.T) {
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	h := newHarness(t, st)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			st.mu.Lock()
			st.agents[fmt.Sprintf("coder%d", i%7)] = &agentState{state: "working", stateAt: time.Now()}
			st.requests = []request{{Tracked: true, State: "requested", Created: time.Now()}}
			st.mu.Unlock()
		}
	}()
	for i := 0; i < 50; i++ {
		h.draw(120, 30)
	}
	wg.Wait()
}
