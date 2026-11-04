package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// request-priority §6: in the tracked section, a group's requests by rank
// then age, and groups by unread, best rank, then oldest; accepted, blocked
// and done count as normal; a group's mark is its highest member's level.
func TestTrackedSectionSortsByPriority(t *testing.T) {
	now := shellNow
	rq := func(task, from, target, state, prio string, ago time.Duration) request {
		return request{Task: task, Tracked: true, ID: task + "-id", Thread: task + "-id", From: from, Target: target,
			State: state, Created: now.Add(-ago), Updated: now.Add(-ago), Priority: prio}
	}
	in := reqInput{now: now, self: "charlie", reqs: []request{
		rq("old-normal", "master", "coder", "requested", "", 50*time.Minute),
		rq("new-high", "master", "coder", "requested", "high", 10*time.Minute),
		rq("oldest-low", "master", "coder", "requested", "low", 90*time.Minute),
		rq("held-high", "master", "coder", "accepted", "high", 5*time.Minute), // in hand: sorts as normal
		rq("qa-old", "master", "qa", "requested", "", 120*time.Minute),
		rq("rev-high", "master", "foureyes", "requested", "high", 1*time.Minute),
	}}
	v := requestGroups(in)
	gs := v.groups[secTracked]
	var order []string
	for _, g := range gs {
		order = append(order, g.Target)
	}
	// Groups: none unread (no cursor): foureyes and coder hold a high, foureyes
	// rank 0 newer, coder rank 0 older (oldest thread 90m) -> coder first, then
	// foureyes; qa normal last.
	if want := []string{"coder", "foureyes", "qa"}; !equalStrings(order, want) {
		t.Fatalf("groups %v, want %v", order, want)
	}
	var tasks []string
	for _, th := range gs[0].Threads {
		tasks = append(tasks, th.Subject)
	}
	if want := []string{"new-high", "old-normal", "held-high", "oldest-low"}; !equalStrings(tasks, want) {
		t.Fatalf("coder's requests %v, want %v", tasks, want)
	}
	if m := groupPriority(gs[0]); m != "high" {
		t.Fatalf("coder's mark %q", m)
	}
	if m := groupPriority(gs[2]); m != "" {
		t.Fatalf("qa's mark %q", m)
	}
	low := reqGroup{Threads: []reqThread{{Kind: kindRequest, Request: &request{Priority: "low"}}}}
	if m := groupPriority(low); m != "low" {
		t.Fatalf("a low group's mark %q", m)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// priorityShell is reqShell with tracked requests to set: one requested
// (single row), one done, and a group of two requested to the same agent.
func priorityShell(t *testing.T, fw *fakeWriter) Model {
	t.Helper()
	m := reqShell(t, 160, 44, fw)
	add := func(task, target, state, prio string, ago time.Duration) request {
		return request{Task: task, Owner: target, Tracked: true, ID: task + "-id", Thread: task + "-id", From: "master", Target: target,
			State: state, Created: shellNow.Add(-ago), Updated: shellNow.Add(-ago), Priority: prio}
	}
	snap := m.snap
	snap.requests = append(append([]request{}, snap.requests...),
		add("solo", "ui-kimi", "requested", "", 30*time.Minute),
		add("twin-a", "sentinel", "requested", "", 20*time.Minute),
		add("twin-b", "sentinel", "requested", "low", 10*time.Minute))
	m.snap = snap
	m.refresh()
	return m
}

func (f *fakeWriter) priorities() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sent {
		if len(s) > 9 && s[:9] == "priority " {
			out = append(out, s)
		}
	}
	return out
}

func TestPriorityKeysOnTheRequestsTab(t *testing.T) {
	fw := &fakeWriter{}
	m := priorityShell(t, fw)
	m.lists.requests.Select("req:solo")
	if !m.pageOffers(ActPriorityUp) || !m.pageOffers(ActPriorityDown) {
		t.Fatal("a single requested request does not offer + and -")
	}
	m = keyRun(t, m, '+')
	if got := fw.priorities(); len(got) != 1 || got[0] != "priority solo high charlie" {
		t.Fatalf("+ wrote %v", got)
	}
	if !strings.Contains(m.ui.flash+noticeTexts(m), "solo: high") {
		t.Fatalf("no outcome: flash %q, notices %q", m.ui.flash, noticeTexts(m))
	}
	// - steps from the level just written (high), not the snapshot's: normal.
	m = keyRun(t, m, '-')
	if got := fw.priorities(); len(got) != 2 || got[1] != "priority solo normal charlie" {
		t.Fatalf("- wrote %v", got)
	}
	// Already at the bound: nothing written, said. A poll that started after
	// the writes reads the board (high) and ends the override.
	snap := m.snap
	for i := range snap.requests {
		if snap.requests[i].Task == "solo" {
			snap.requests[i].Priority = "high"
		}
	}
	later := time.Now().Add(time.Minute)
	m = step(t, m, pollMsg{snap: snap, at: later, started: later})
	m.lists.requests.Select("req:solo")
	m = keyRun(t, m, '+')
	if got := fw.priorities(); len(got) != 2 || !strings.Contains(m.ui.flash, "already high") {
		t.Fatalf("+ on high wrote %v, flash %q", got, m.ui.flash)
	}
}

func TestPriorityKeysRefused(t *testing.T) {
	fw := &fakeWriter{}
	m := priorityShell(t, fw)
	if requestGroupKey(m) == "" {
		t.Fatal("fixture: no group of tracked requests")
	}
	cases := []struct{ key, why string }{
		{requestGroupKey(m), "open the group to set one request's priority"},
		{hasKey(m, "thr:"), "only a tracked request has a priority"},
		{"req:busmon-group-a", ""}, // accepted: allowed (open question 4)
	}
	for _, c := range cases {
		m.lists.requests.Select(c.key)
		m.ui.flash = ""
		m = keyRun(t, m, '+')
		if c.why == "" {
			continue
		}
		if !strings.Contains(m.ui.flash, c.why) || m.pageOffers(ActPriorityUp) {
			t.Fatalf("%s: flash %q, offered %v", c.key, m.ui.flash, m.pageOffers(ActPriorityUp))
		}
	}
	if got := fw.priorities(); len(got) != 1 || got[0] != "priority busmon-group-a high charlie" {
		t.Fatalf("writes %v", got)
	}
	// A done request: delivered, in review.
	snap := m.snap
	snap.requests = append(append([]request{}, snap.requests...), request{Task: "shipped", Tracked: true, ID: "s-id", Thread: "s-id",
		From: "master", Target: "coder", State: "done", Created: shellNow, Updated: shellNow})
	m.snap = snap
	m.ui.reqOpen.folded = true
	m.refresh()
	m.lists.requests.Select("req:shipped")
	m = keyRun(t, m, '-')
	if !strings.Contains(m.ui.flash, "delivered: in review, its priority orders nothing") || len(fw.priorities()) != 1 {
		t.Fatalf("done: flash %q, writes %v", m.ui.flash, fw.priorities())
	}
	// Another panel: + is not this key's.
	m.focusPanel(PanelAgents)
	if m.pageOffers(ActPriorityUp) {
		t.Fatal("offered on the Agents panel")
	}
}

func TestPriorityOnTheRequestPage(t *testing.T) {
	fw := &fakeWriter{}
	m := priorityShell(t, fw)
	snap := m.snap
	for i := range snap.requests {
		if snap.requests[i].Task == "solo" {
			snap.requests[i].Priority, snap.requests[i].PriorityBy, snap.requests[i].PriorityAt = "high", "charlie", shellNow.Add(-time.Hour)
		}
	}
	m.snap = snap
	m.refresh()
	m.lists.requests.Select("req:solo")
	m = keys(t, m, kEnter)
	p := m.rd.top()
	if p == nil || p.kind != ovRequest {
		t.Fatal("no request page")
	}
	var text strings.Builder
	for _, l := range p.body {
		text.WriteString(l.text() + "\n")
	}
	if want := "Priority: high " + GlyphNone + " set by charlie " + shellNow.Add(-time.Hour).Local().Format("15:04"); !strings.Contains(text.String(), want) {
		t.Fatalf("page lacks %q:\n%s", want, text.String())
	}
	if !m.pageOffers(ActPriorityDown) {
		t.Fatal("the page does not offer -")
	}
	m = keyRun(t, m, '-')
	if got := fw.priorities(); len(got) != 1 || got[0] != "priority solo normal charlie" {
		t.Fatalf("- on the page wrote %v", got)
	}
}

func noticeTexts(m Model) string {
	var b strings.Builder
	for _, n := range m.notice.queue {
		b.WriteString(n.text + "\n")
	}
	return b.String()
}

// keyRun presses r and runs the command it returns, feeding its message back
// (the writer's call happens in a command).
func keyRun(t *testing.T, m Model, r rune) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	m = next.(Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m = step(t, m, msg)
		}
	}
	return m
}

// requestGroupKey is the Requests row of a group of tracked requests.
func requestGroupKey(m Model) string {
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, "grp:") && strings.HasSuffix(k, "|"+kindRequest) {
			return k
		}
	}
	return ""
}

// The marks at the three tiers: ↑ high, ↓ low, a blank for normal, the kinds
// in one column (request-priority §6).
func TestRequestsPriorityGoldens(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{190, 44}, {120, 40}, {80, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := priorityShell(t, nil)
			m = step(t, m, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			snap := m.snap
			for i := range snap.requests {
				switch snap.requests[i].Task {
				case "solo":
					snap.requests[i].Priority = "high"
				case "twin-a": // both low: the group shows its highest member's mark
					snap.requests[i].Priority = "low"
				}
			}
			m.snap = snap
			m.refresh()
			m.paint = newPainter(th)
			plain := m.grid().plain()
			if !strings.Contains(plain, GlyphUp+" req") || !strings.Contains(plain, GlyphDownArrow+" req") {
				t.Fatalf("marks missing:\n%s", plain)
			}
			checkGolden(t, fmt.Sprintf("requests-priority-%dx%d.txt", sz[0], sz[1]), plain+"\n")
		})
	}
}

// An all-low group sorts after a normal one, whatever their ages
// (reviewer-codex: the rank started at normal).
func TestAllLowGroupSortsAfterNormal(t *testing.T) {
	now := shellNow
	rq := func(task, target, prio string, ago time.Duration) request {
		return request{Task: task, Tracked: true, ID: task + "-id", Thread: task + "-id", From: "master", Target: target,
			State: "requested", Created: now.Add(-ago), Updated: now.Add(-ago), Priority: prio}
	}
	v := requestGroups(reqInput{now: now, self: "charlie", reqs: []request{
		rq("low-a", "qa", "low", 90*time.Minute), rq("low-b", "qa", "low", 80*time.Minute),
		rq("plain", "coder", "", 5*time.Minute),
	}})
	gs := v.groups[secTracked]
	if len(gs) != 2 || gs[0].Target != "coder" || gs[1].Target != "qa" {
		t.Fatalf("order %v", []string{gs[0].Target, gs[1].Target})
	}
	if r := groupRank(gs[1]); r != 2 {
		t.Fatalf("all-low group's rank %d", r)
	}
}

// Steps follow the person's own writes, not the last poll (reviewer-codex):
// low, +, its answer, + reaches high before any poll; a press while a write
// is in flight is said and sends nothing; the override ends with a poll that
// started after the write.
func TestPriorityStepsFollowTheirOwnWrites(t *testing.T) {
	fw := &fakeWriter{}
	m := priorityShell(t, fw)
	snap := m.snap
	for i := range snap.requests {
		if snap.requests[i].Task == "solo" {
			snap.requests[i].Priority = "low"
		}
	}
	m.snap = snap
	m.refresh()
	m.lists.requests.Select("req:solo")

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: '+', Text: "+"}))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("+ sent nothing")
	}
	m = keys(t, m, '+') // in flight: refused
	if !strings.Contains(m.ui.flash, "on its way") {
		t.Fatalf("second press while in flight: %q", m.ui.flash)
	}
	m = step(t, m, cmd()) // the first write's answer
	m = keyRun(t, m, '+')
	m = keyRun(t, m, '-')
	got := fw.priorities()
	want := []string{"priority solo normal charlie", "priority solo high charlie", "priority solo normal charlie"}
	if !equalStrings(got, want) {
		t.Fatalf("writes %v, want %v", got, want)
	}
	// A poll that started before the last write keeps the override; one after ends it.
	written := m.ui.prioSeen["solo"].at
	m = step(t, m, pollMsg{snap: m.snap, at: written, started: written.Add(-time.Second)})
	if _, ok := m.ui.prioSeen["solo"]; !ok {
		t.Fatal("an older poll ended the override")
	}
	m = step(t, m, pollMsg{snap: m.snap, at: written.Add(2 * time.Second), started: written.Add(time.Second)})
	if _, ok := m.ui.prioSeen["solo"]; ok {
		t.Fatal("a newer poll kept the override")
	}
}
