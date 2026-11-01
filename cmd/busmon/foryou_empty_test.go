package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/bus"
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
	text := plainOf(forYouEmpty(s, 0))
	// In progress names the agent and its current task from the facts (its plan
	// task in progress), never its status message (the person saw a merged task).
	for _, w := range []string{"Nothing expected from you.", "In progress: coder " + GlyphNone + " pages-4", "Next for you: Decide: notifications order"} {
		if !strings.Contains(text, w) {
			t.Errorf("empty state lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, "foureyes") {
		t.Errorf("an idle agent listed as in progress:\n%s", text)
	}
	// Nothing in progress, no plan: it still says what it knows.
	text = plainOf(forYouEmpty(snapshot{}, 0))
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
	empty := forYouEmpty(snapshot{}, 0)
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

// "Nothing expected" only when nothing is: a request owed to the person, or
// a plan task of theirs waiting, is named instead, and "Next for you" is never
// a task that is already due (foureyes).
func TestForYouEmptyNamesWhatIsOwed(t *testing.T) {
	s := snapshot{}
	s.planState.loaded = true
	s.planState.rows = []plan.Row{
		{Task: plan.Task{ID: "pick", Title: "Pick the notifications order", Owner: plan.Person}, State: plan.Waiting},
		{Task: plan.Task{ID: "tag", Title: "Reinstall and tag", Owner: plan.Person}, State: plan.Upcoming},
	}
	text := plainOf(forYouEmpty(s, 2))
	// The waiting plan task is a For you item of its own (rankPlan), counted in
	// the title, not an empty-state line.
	if items := forYou(s, nil); len(items) != 1 || items[0].Rank != rankPlan || items[0].Key != "plan:pick" || len(decisions(items)) != 1 {
		t.Errorf("the waiting plan task as an item: %+v", items)
	}
	for _, w := range []string{"Owed by you: 2 requests (Requests)", "Next for you: Reinstall and tag"} {
		if !strings.Contains(text, w) {
			t.Errorf("lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, "Nothing expected") {
		t.Errorf("says nothing is expected while something is:\n%s", text)
	}
}

// With the panel on screen the recap keeps what the panel does not list
// (owed directives, blocked agents, plan tasks, read errors) under its
// heading (foureyes).
func TestRecapKeepsWhatThePanelDoesNotList(t *testing.T) {
	r := recapModel{Rows: []recapRow{
		{Section: recapForYou, Title: recapForYou},
		{Key: "ask:1:q", Section: recapForYou, Title: "master asks · go?", Selectable: true},
		{Key: "cmd:9-0", Section: recapForYou, Title: "master → charlie · 2h", Selectable: true},
		{Key: "blocked:docs", Section: recapForYou, Title: "docs blocked", Selectable: true},
		{Section: recapForYou, Title: "Open asks unavailable"},
	}}
	var b strings.Builder
	for _, row := range recapForScreen(r, true, nil).Rows {
		b.WriteString(row.Title + "\n")
	}
	got := b.String()
	for _, w := range []string{recapForYou + "\n", "master → charlie", "docs blocked", "Open asks unavailable"} {
		if !strings.Contains(got, w) {
			t.Errorf("the recap lost %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "master asks") {
		t.Errorf("an ask the panel lists shows twice:\n%s", got)
	}
}

// One request owed reads in the singular (foureyes' nit: owed > 1 survived).
func TestForYouEmptyOneRequestOwed(t *testing.T) {
	text := plainOf(forYouEmpty(snapshot{}, 1))
	if !strings.Contains(text, "Owed by you: 1 request (Requests)") || strings.Contains(text, "Nothing expected") {
		t.Errorf("one request owed:\n%s", text)
	}
}

// The recap page re-filters its For you section at every build for the
// current tier: opened wide then narrowed, the section comes back; opened
// narrow then widened beside the panel, it goes (reviewer-codex).
func TestRecapRefiltersOnResize(t *testing.T) {
	rec := recapModel{Rows: []recapRow{
		{Section: recapForYou, Title: recapForYou},
		{Key: "ask:1:q", Section: recapForYou, Title: "master asks · go?", Selectable: true},
		{Section: "Agents", Title: "Agents"},
		{Key: "agent:coder", Section: "Agents", Title: "coder · working", Selectable: true},
	}}
	open := func(w int) Model {
		m := reqShell(t, w, 40, nil)
		m.gotRecap(recapMsg{model: rec})
		m.maybeOpenRecap()
		if p := m.rd.top(); p == nil || p.kind != ovRecap {
			t.Fatalf("recap page not open at %d columns", w)
		}
		return m
	}
	has := func(m Model) bool {
		for _, row := range m.rd.top().recap.Rows {
			if row.Key == "ask:1:q" {
				return true
			}
		}
		return false
	}
	m := open(140)
	if has(m) {
		t.Fatalf("wide: the recap repeats an ask the panel lists")
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 70, Height: 40}); m.layout.Tier != TierNarrow || !has(m) {
		t.Errorf("wide then narrow (tier %v): the ask is missing from the recap", m.layout.Tier)
	}
	m = open(70)
	if !has(m) {
		t.Fatalf("narrow: the recap lost the ask")
	}
	m.rd.top().sel = 1 // the cursor on the ask
	if m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40}); has(m) {
		t.Errorf("narrow then wide: the ask shows twice")
	}
	if p := m.rd.top(); p.sel < 0 || p.sel >= len(p.recap.Rows) || !p.recap.Rows[p.sel].Selectable {
		t.Errorf("the cursor left the selectable rows: %d", p.sel)
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 70, Height: 40}); !has(m) {
		t.Errorf("wide again then narrow: the ask is missing")
	}
}

// The For you frame is sized from the Requests view of this fill, never the
// previous one: when the owed count changes, the empty state's rows and the
// frame agree at once (reviewer-codex).
func TestForYouSizedFromTheCurrentOwedCount(t *testing.T) {
	m := reqShell(t, 100, 40, nil)
	snap := m.snap
	snap.planState.loaded = true
	snap.planState.rows = []plan.Row{{Task: plan.Task{ID: "pick", Title: "Pick the order", Owner: plan.Person}, State: plan.Upcoming}} // a waiting one is a For you item now
	plain := snap.cmdHistory
	owed := append(append([]bus.Event(nil), plain...), bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-0", shellNow.Add(-time.Minute).UnixMilli()),
		Type: bus.CmdDirective, From: "master", Target: "charlie", Message: "pick one"})
	check := func(label string) {
		t.Helper()
		if m.layout.Tier != TierMedium {
			t.Fatalf("%s: tier %v", label, m.layout.Tier)
		}
		if want := strings.Contains(label, "owed"); (m.lists.reqView.count(secOwed) > 0) != want {
			t.Fatalf("%s: owed count %d", label, m.lists.reqView.count(secOwed))
		}
		if n := len(m.lists.forYou.items); m.layout.ForYouRows != n {
			t.Errorf("%s: frame sized for %d rows, the empty state has %d", label, m.layout.ForYouRows, n)
		}
		if screen := m.grid().plain(); !strings.Contains(screen, "Next for you:") { // owed and waiting: four lines, all drawn at the medium tier (foureyes)
			t.Errorf("%s: the medium tier cuts the empty state:\n%s", label, screen)
		}
	}
	snap.cmdHistory = owed
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	check("owed")
	snap.cmdHistory = plain
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	check("nothing")
	snap.cmdHistory = owed
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	check("owed again")
}

// A resize keeps the cursor on its own row when a row before it is dropped
// (foureyes: the key branch of recapKeepCursor was unpinned).
func TestRecapCursorKeepsItsRowOnResize(t *testing.T) {
	rec := recapModel{Rows: []recapRow{
		{Section: recapForYou, Title: recapForYou},
		{Key: "ask:1:q", Section: recapForYou, Title: "master asks · go?", Selectable: true},
		{Key: "cmd:9-0", Section: recapForYou, Title: "master → charlie · 2h", Selectable: true},
		{Section: "Agents", Title: "Agents"},
		{Key: "agent:coder", Section: "Agents", Title: "coder · working", Selectable: true},
	}}
	m := reqShell(t, 70, 40, nil)
	m.gotRecap(recapMsg{model: rec})
	m.maybeOpenRecap()
	p := m.rd.top()
	p.sel = 2 // the owed directive, after the ask the panel will list
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	if p = m.rd.top(); p.recap.Rows[p.sel].Key != "cmd:9-0" {
		t.Errorf("the cursor moved to %q", p.recap.Rows[p.sel].Key)
	}
}

// The facts win over a stale message: an accepted board task names what an
// agent works on; with none, only its name.
func TestForYouEmptyInProgressFromFacts(t *testing.T) {
	s := snapshot{agents: map[string]*agentState{"coder": {state: "working", message: "codex-review-auth: boot reads"}},
		board: map[string]bus.BoardEntry{"history": {Owner: "coder", State: "accepted"}, "a-merged": {Owner: "coder", State: "done"}}}
	text := plainOf(forYouEmpty(s, 0))
	if !strings.Contains(text, "In progress: coder "+GlyphNone+" history") || strings.Contains(text, "codex-review-auth") {
		t.Fatalf("in progress:\n%s", text)
	}
	// A finished board task is never in progress: the plan's task in progress
	// is named, else only the agent.
	for _, st := range []string{"done", "complete", "blocked"} {
		s.board = map[string]bus.BoardEntry{"a-merged": {Owner: "coder", State: st}}
		s.planState = planState{loaded: true, rows: []plan.Row{{Task: plan.Task{ID: "auto-state", Owner: "coder"}, State: plan.InProgress}}}
		if text := plainOf(forYouEmpty(s, 0)); !strings.Contains(text, "In progress: coder "+GlyphNone+" auto-state") || strings.Contains(text, "a-merged") {
			t.Fatalf("board %s:\n%s", st, text)
		}
		s.planState = planState{}
		if text := plainOf(forYouEmpty(s, 0)); !strings.Contains(text, "In progress: coder\n") {
			t.Fatalf("board %s, no plan: %s", st, text)
		}
	}
}

// Every empty-state line that names something opens it: the agent's page, the
// plan task's page; a waiting plan task item opens its page too.
func TestForYouEmptyRowsOpen(t *testing.T) {
	m := reqShell(t, 120, 40, nil)
	snap := m.snap
	snap.agents["coder"].state = "working"
	snap.planState.loaded = true
	snap.planState.rows = []plan.Row{{Task: plan.Task{ID: "tag", Title: "Reinstall and tag", Owner: plan.Person}, State: plan.Upcoming}}
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	for key, want := range map[string]overlayKind{"empty:agent:coder": ovAgent, "empty:plan:tag": ovPlanTask} {
		n := m
		n.focusPanel(PanelForYou)
		n.lists.forYou.Select(key)
		if n.lists.forYou.Selected() != key {
			t.Fatalf("%s is not a selectable row", key)
		}
		n.open(PanelForYou)
		if top := n.rd.top(); top == nil || top.kind != want {
			t.Errorf("%s opened %+v", key, top)
		}
	}
	snap.planState.rows[0].State = plan.Waiting
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	m.focusPanel(PanelForYou)
	m.lists.forYou.Select("plan:tag")
	m.open(PanelForYou)
	if top := m.rd.top(); top == nil || top.kind != ovPlanTask || top.key != "tag" {
		t.Errorf("the plan item opened %+v", top)
	}
	// From the reminder too: Enter opens the task's page, never a compose
	// window to a recipient named after the item; i writes to nobody.
	for _, it := range decisions(m.forYou) {
		if it.Rank != rankPlan {
			continue
		}
		n := m
		n.rd = newReader()
		n.ui.reminder = &reminderDialog{items: []attentionItem{it}}
		n.reminderKey(Key{Code: keyEnter})
		if top := n.rd.top(); top == nil || top.kind != ovPlanTask || top.key != "tag" || n.ui.compose != nil {
			t.Errorf("reminder Enter opened %+v (compose %v)", top, n.ui.compose != nil)
		}
		n = m
		n.rd = newReader()
		n.ui.reminder = &reminderDialog{items: []attentionItem{it}}
		n.reminderKey(Key{Code: keyRune, Rune: 'i'})
		if n.ui.compose != nil {
			t.Error("reminder i composed to a plan item")
		}
		return
	}
	t.Fatal("no plan item among the decisions")
}
