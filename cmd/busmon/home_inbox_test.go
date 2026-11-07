package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/plan"
)

// home-inbox-drawer §3, step 1 (home-inbox): For you is an inbox that can
// empty. Plan tasks leave it; one muted pointer stays; unread lines leave the
// panel, not the model.

func personTask(id, title string, st plan.State) plan.Row {
	return plan.Row{Task: plan.Task{ID: id, Title: title, Owner: plan.Person}, State: st}
}

func planSnap(rows ...plan.Row) snapshot {
	s := snapshot{}
	s.planState.loaded = true
	s.planState.rows = rows
	return s
}

// §9 Membership: no plan row, no decision; the pointer is the first Waiting
// person task in file order, then Blocked, then Upcoming; none, or no plan,
// no pointer.
func TestForYouHasNoPlanRows(t *testing.T) {
	s := planSnap(
		plan.Row{Task: plan.Task{ID: "c1", Title: "build", Owner: "coder"}, State: plan.Waiting},
		personTask("a", "first", plan.Waiting),
		personTask("b", "second", plan.Waiting),
		personTask("c", "third", plan.Waiting),
	)
	items := forYou(s, nil)
	for _, it := range items {
		if strings.HasPrefix(it.Key, "plan") {
			t.Errorf("a plan row in For you: %+v", it)
		}
	}
	if n := len(decisions(items)); n != 0 {
		t.Errorf("decisions count %d plan tasks", n)
	}
	if r, ok := nextPlanTask(s.planState); !ok || r.Task.ID != "a" {
		t.Errorf("pointer %q %v, want a", r.Task.ID, ok)
	}
}

func TestNextPlanTaskOrder(t *testing.T) {
	for _, c := range []struct {
		name string
		s    snapshot
		want string
	}{
		{"waiting first", planSnap(personTask("u", "u", plan.Upcoming), personTask("b", "b", plan.Blocked), personTask("w", "w", plan.Waiting)), "w"},
		{"then blocked", planSnap(personTask("u", "u", plan.Upcoming), personTask("b", "b", plan.Blocked)), "b"},
		{"then upcoming", planSnap(personTask("d", "d", plan.Done), personTask("u", "u", plan.Upcoming)), "u"},
		{"an agent's task never", planSnap(plan.Row{Task: plan.Task{ID: "c", Owner: "coder"}, State: plan.Waiting}), ""},
		{"none", planSnap(personTask("d", "d", plan.Done)), ""},
		{"no plan", snapshot{}, ""},
	} {
		r, ok := nextPlanTask(c.s.planState)
		if got := map[bool]string{true: r.Task.ID}[ok]; got != c.want {
			t.Errorf("%s: pointer %q, want %q", c.name, got, c.want)
		}
	}
}

// §9 The panel: no unread row, the pointer last; its label equals the
// strip's words for the same items.
func TestForYouPanelRowsAndLabel(t *testing.T) {
	m := rowsFixture(t, false, false)
	if unreadItems(m.forYou) == 0 {
		t.Fatal("fixture has no unread line")
	}
	items := m.lists.forYou.items
	for _, it := range items {
		if strings.HasPrefix(it.Key, "unread:") {
			t.Errorf("an unread row in the panel: %q", it.Key)
		}
	}
	last := items[len(items)-1]
	if last.Key != planNextPrefix+"p1" || !last.Selectable {
		t.Errorf("last row %q, want the pointer", last.Key)
	}
	for _, sg := range last.Line {
		if sg.Role != RoleMuted {
			t.Errorf("the pointer is not muted: %+v", last.Line)
		}
	}
	q := stripOf(m.forYou)
	if q.decide == 0 || q.merge == 0 {
		t.Fatalf("fixture strip %+v", q)
	}
	if got, want := m.lists.forYou.label.text(), itoa(q.decide)+" decide "+GlyphNone+" "+itoa(q.merge)+" merge"; got != want {
		t.Errorf("label %q, want %q", got, want)
	}
	if lbl := forYouLabel(queueStrip{merge: 2, unread: 3}); lbl != "2 merge" {
		t.Errorf("a part at 0 is left out: %q", lbl)
	}
	if lbl := forYouLabel(queueStrip{unread: 3}); lbl != "" {
		t.Errorf("nothing to decide or merge: %q", lbl)
	}
}

// §9 The pointer is not a decision: never in the reminder or the walk;
// Enter opens its task page.
func TestPlanPointerIsNotADecision(t *testing.T) {
	m := rowsFixture(t, false, false)
	for _, it := range decisions(m.forYou) {
		if strings.HasPrefix(it.Key, "plan") {
			t.Errorf("a decision %q", it.Key)
		}
	}
	m = keys(t, m, kT)
	for i := 0; i < 30 && m.rd.open() && m.rd.top().kind != ovTriageEnd; i++ {
		if it, ok := m.walkItem(); ok && strings.HasPrefix(it.Key, "plan") {
			t.Fatalf("the walk visits %q", it.Key)
		}
		m = keys(t, m, 'n')
	}
	m = rowsFixture(t, false, false)
	m.focusPanel(PanelForYou)
	m.lists.forYou.Select(planNextPrefix + "p1")
	m.refresh()
	if w, _ := rowHint(PanelForYou, m.selectedItem(PanelForYou)); w != "task" {
		t.Errorf("rowHint %q", w)
	}
	m = keys(t, m, kEnter)
	if topKind(t, m) != ovPlanTask || m.rd.top().key != "p1" {
		t.Fatalf("enter on the pointer: %v %q", m.rd.top().kind, m.rd.top().key)
	}
}

// §3.3 Empty For you: what is owed or "Nothing expected", then the pointer
// (the same producer); no plan task, no pointer line.
func TestForYouEmptyThenPointer(t *testing.T) {
	m := rowsFixture(t, false, true)
	var keys []string
	for _, it := range m.lists.forYou.items {
		keys = append(keys, it.Key)
	}
	if len(keys) != 2 || keys[0] != "" || keys[1] != planNextPrefix+"p1" {
		t.Errorf("empty panel keys %q", keys)
	}
	if got := plainOf(forYouEmpty(planSnap(personTask("w", "pick the order", plan.Waiting)), 0)); got != "Nothing expected from you.\nNext for you: pick the order" {
		t.Errorf("a Waiting task is the pointer, not a reason to say something is owed:\n%s", got)
	}
	if got := plainOf(forYouEmpty(snapshot{}, 0)); got != "Nothing expected from you." {
		t.Errorf("no plan, no pointer:\n%s", got)
	}
}

// At the medium tier, unfocused, the pointer stays under "+N more" and the
// panel's height holds it.
func TestForYouMediumKeepsThePointer(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.focusPanel(PanelAgents)
	m.refresh()
	items := m.lists.forYou.items
	if len(items) < 2 || items[len(items)-2].Key != "more" || items[len(items)-1].Key != planNextPrefix+"p1" {
		var ks []string
		for _, it := range items {
			ks = append(ks, it.Key)
		}
		t.Fatalf("medium rows %q", ks)
	}
	if m.layout.ForYouRows != len(items) {
		t.Errorf("layout rows %d, panel rows %d", m.layout.ForYouRows, len(items))
	}
}

// The panel draws no unread line, so the recap keeps its unread rows even
// with For you on screen (recapInPanel, foureyes' "only what the panel itself
// lists").
func TestRecapKeepsUnreadRowsBesideThePanel(t *testing.T) {
	r := recapModel{Rows: []recapRow{
		{Section: recapForYou, Title: recapForYou},
		{Key: "unread:1-0", Section: recapForYou, Title: "unread: dispatch paused", Selectable: true},
		{Key: "ask:2-0:q", Section: recapForYou, Title: "master asks", Selectable: true},
	}}
	var titles []string
	for _, row := range recapForScreen(r, true, nil).Rows {
		titles = append(titles, row.Title)
	}
	if got := strings.Join(titles, "|"); got != recapForYou+"|unread: dispatch paused" {
		t.Errorf("recap with the panel on screen: %q", got)
	}
}

// At the wide tier For you is as wide as its widest row: a pointer longer
// than every decision row widens it too, so its title is not cut.
func TestForYouWidthHoldsThePointer(t *testing.T) {
	m := rowsFixture(t, false, false)
	s := m.snap
	s.planState.rows[0].Task.Title = strings.Repeat("a long plan task title ", 2)
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	p, ok := planPointer(m.snap)
	if !ok {
		t.Fatal("no pointer")
	}
	if got, want := m.counts().ForYouWidth, p.line.Width()+listGutter+2; got < want {
		t.Errorf("For you's natural width %d, the pointer needs %d", got, want)
	}
}
