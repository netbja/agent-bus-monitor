package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

const busmonPlan = `{"plan":"v0.22","goal":"one batch","tasks":[
 {"id":"roles","title":"Specialist roles","owner":"coder","board":"t"},
 {"id":"reload","title":"Client reload","owner":"coder","board":"reload-impl"},
 {"id":"deploy","title":"Deploy change","owner":"coder","mission":"v22/deploy"},
 {"id":"release","title":"Tag and install","owner":"person","after":["roles","reload"]}]}`

// planFake adds the plan reads to fakeReader, counted like the others.
type planFake struct {
	*fakeReader
	rec      *bus.PlanRecord
	facts    map[string]bus.PlanFact
	ledger   []bus.Verdict
	afters   []string // the cursor of each VerdictsAfter call
	missions map[string]string
	failPlan bool
}

func (f *planFake) Plan(context.Context) (bus.PlanRecord, bool, error) {
	f.n("Plan")
	if f.failPlan {
		return bus.PlanRecord{}, false, errors.New("timeout")
	}
	if f.rec == nil {
		return bus.PlanRecord{}, false, nil
	}
	return *f.rec, true, nil
}
func (f *planFake) PlanFacts(context.Context) (map[string]bus.PlanFact, error) {
	f.n("PlanFacts")
	if f.failPlan {
		return nil, errors.New("timeout")
	}
	return f.facts, nil
}
func (f *planFake) VerdictsAfter(_ context.Context, after string, count int64) ([]bus.Verdict, error) {
	f.n("VerdictsAfter")
	f.afters = append(f.afters, after)
	var out []bus.Verdict
	for _, v := range f.ledger {
		if (after == "" || plan.IDLess(after, v.ID)) && int64(len(out)) < count {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *planFake) ReadMissionHash(_ context.Context, key string) (map[string]string, error) {
	f.n("ReadMissionHash")
	return f.missions, nil
}

func newPlanFake(withPlan bool) *planFake {
	f := &planFake{fakeReader: &fakeReader{calls: map[string]int{}}, facts: map[string]bus.PlanFact{},
		missions: map[string]string{"deploy": `{"id":"deploy","state":"checking"}`}}
	if withPlan {
		f.rec = &bus.PlanRecord{Path: "docs/plans/v0.22.json", Commit: "abcdef0123", LoadedAt: time.Now().Add(-3 * time.Hour).UnixMilli(),
			Tasks: []string{"roles", "reload", "deploy", "release"}, File: busmonPlan}
	}
	return f
}

func newPlanShared() *shared {
	return &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
}

func TestPlanReadsOnlyOnRequestTicks(t *testing.T) {
	f := newPlanFake(false)
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	if f.calls["Plan"] != 1 || f.calls["PlanFacts"] != 0 || f.calls["VerdictsAfter"] != 0 {
		t.Fatalf("no plan loaded: one GET only, got %v", f.calls)
	}
	f.calls = map[string]int{}
	pollOnce(context.Background(), f, st, 2)
	if f.calls["Plan"] != 0 {
		t.Fatalf("a light tick read the plan: %v", f.calls)
	}
	if st.plan.loaded {
		t.Fatal("no plan, yet loaded")
	}

	f = newPlanFake(true)
	st = newPlanShared()
	f.ledger = []bus.Verdict{{ID: "5-0", Subject: "t", Author: "coder", Reviewer: "foureyes", Decision: "approve"}}
	pollOnce(context.Background(), f, st, 1)
	if f.calls["Plan"] != 1 || f.calls["PlanFacts"] != 1 || f.calls["VerdictsAfter"] != 1 || f.calls["ReadMissionHash"] != 1 {
		t.Fatalf("a plan tick: %v", f.calls)
	}
	f.ledger = append(f.ledger, bus.Verdict{ID: "6-0", Subject: "reload-impl", Author: "coder", Reviewer: "foureyes", Decision: "reject"})
	pollOnce(context.Background(), f, st, 6)
	if got := f.afters; len(got) != 2 || got[0] != "" || got[1] != "5-0" {
		t.Fatalf("the ledger is read from the last seen id: %v", got)
	}
	rows := st.plan.rows
	if len(rows) != 4 {
		t.Fatalf("rows %+v", rows)
	}
	want := map[string]plan.State{"roles": plan.Waiting, "reload": plan.InReview, "deploy": plan.InProgress, "release": plan.Upcoming}
	for _, r := range rows {
		if r.State != want[r.Task.ID] {
			t.Errorf("%s: %s want %s", r.Task.ID, r.State, want[r.Task.ID])
		}
	}
}

func TestPlanFailedReadKeepsRowsAndMarksThem(t *testing.T) {
	f := newPlanFake(true)
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	if len(st.plan.rows) != 4 || st.plan.partial {
		t.Fatalf("first read: %+v", st.plan)
	}
	f.failPlan = true
	pollOnce(context.Background(), f, st, 6)
	if len(st.plan.rows) != 4 || !st.plan.partial {
		t.Fatalf("a failed read keeps the rows and marks them: %+v", st.plan)
	}

	// A plan never read successfully shows nothing, never an empty plan.
	f = newPlanFake(true)
	f.failPlan = true
	st = newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	if st.plan.loaded {
		t.Fatal("a failed first read showed a plan")
	}
}

func TestPlanHeaderItem(t *testing.T) {
	now := time.Now()
	sum := planSummary{name: "v0.22", done: 3, total: 9, person: 1}
	got := planSeg(sum, now).text()
	if got != "plan v0.22 3/9 "+GlyphHuman+" 1" {
		t.Fatalf("header %q", got)
	}
	sum.partial = true
	sum.person = 0
	if got := planSeg(sum, now).text(); got != "plan v0.22 3/9?" {
		t.Fatalf("partial header %q", got)
	}
}

func TestPlanOverlayGroupsInOrder(t *testing.T) {
	f := newPlanFake(true)
	f.facts = map[string]bus.PlanFact{"roles": {PR: 102, Done: time.Now().Add(-2 * time.Hour).UnixMilli()}}
	f.ledger = []bus.Verdict{{ID: "6-0", Subject: "reload-impl", Author: "coder", Reviewer: "foureyes", Decision: "reject"}}
	f.fakeReader.asks = []bus.Ask{{ID: "9-0", From: "master", Title: "Tag now?", Task: "release", State: "open"}}
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	body, plain, _ := planOverlay(st.plan, -1, false, time.Now(), 100)
	order := []string{"waiting for you", "in review", "in progress", "done (1)"}
	last := -1
	for _, w := range order {
		i := strings.Index(plain, w)
		if i < 0 || i < last {
			t.Fatalf("group %q missing or out of order:\n%s", w, plain)
		}
		last = i
	}
	if strings.Contains(plain, "merged (recorded") {
		t.Fatalf("done is collapsed to a count:\n%s", plain)
	}
	_, plain, _ = planOverlay(st.plan, -1, true, time.Now(), 100)
	if !strings.Contains(plain, "merged (recorded 2h ago)") || !strings.Contains(plain, "#102") {
		t.Fatalf("expanded done:\n%s", plain)
	}
	if !strings.Contains(body, GlyphHuman) || !strings.Contains(plain, "abcdef0") {
		t.Fatalf("glyphs and commit:\n%s", body)
	}
}

func TestPlanOverlayKeys(t *testing.T) {
	f := newPlanFake(true)
	f.ledger = []bus.Verdict{{ID: "6-0", Subject: "reload-impl", Author: "coder", Reviewer: "foureyes", Decision: "reject"}}
	f.facts = map[string]bus.PlanFact{"roles": {Done: time.Now().Add(-time.Hour).UnixMilli()}}
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	h := newHarness(t, st)
	h.draw(140, 40)
	h.key(runeKey('P'))
	if h.u.ov != ovPlan {
		t.Fatalf("P did not open the plan: %v", h.u.ov)
	}
	scr := h.draw(140, 40)
	if !strings.Contains(scr, "Plan") || !strings.Contains(scr, "Client reload") {
		t.Fatalf("plan overlay:\n%s", scr)
	}
	// The first item is selected; j moves to the next, Enter opens its detail.
	first := h.u.planItems[h.u.planSel].task
	h.key(runeKey('j'))
	second := h.u.planItems[h.u.planSel].task
	if first == second || second == "" {
		t.Fatalf("j did not move: %q %q", first, second)
	}
	h.key(key(tcell.KeyEnter))
	if h.u.ov != ovPlanTask || !strings.Contains(h.u.ovPlain, "state") {
		t.Fatalf("enter did not open the task: %v %q", h.u.ov, h.u.ovPlain)
	}
	h.key(key(tcell.KeyEscape))
	if h.u.ov != ovPlan || h.u.planItems[h.u.planSel].task != second {
		t.Fatalf("esc from a task returns to the plan, selection kept: %v", h.u.ov)
	}
	// The done group expands on Enter.
	for i, it := range h.u.planItems {
		if it.toggle {
			h.u.planSel = i
		}
	}
	h.key(key(tcell.KeyEnter))
	if !h.u.planExpanded || !strings.Contains(h.u.ovPlain, "merged (recorded") {
		t.Fatalf("enter on done did not expand: %q", h.u.ovPlain)
	}
	h.key(runeKey('y'))
	h.key(key(tcell.KeyEscape))
	if h.u.ov != ovNone {
		t.Fatalf("esc did not close: %v", h.u.ov)
	}
	// With no plan, P says so and opens nothing.
	st2 := newPlanShared()
	h2 := newHarness(t, st2)
	h2.draw(140, 40)
	h2.key(runeKey('P'))
	if h2.u.ov != ovNone || !strings.Contains(h2.u.flash, "no plan") {
		t.Fatalf("P with no plan: %v %q", h2.u.ov, h2.u.flash)
	}
}

// hostilePlan is a plan with a task in every state, hostile titles and notes
// at the length limits, for the alignment suite.
func hostilePlan(now time.Time) planState {
	long := strings.Repeat("w", 31)
	tasks := []string{}
	var specs []string
	for i, s := range []string{"wait", "block", "rev", "prog", "up", "unk", "done", "done2"} {
		id := fmt.Sprintf("%s%s", s, long)[:32]
		tasks = append(tasks, id)
		owner := "coder"
		if i == 0 {
			owner = "person"
		}
		extra := fmt.Sprintf(`,"board":"%s"`, id)
		if s == "unk" {
			extra = `,"mission":"c/unk"`
		}
		if owner == "person" {
			extra = ""
		}
		specs = append(specs, fmt.Sprintf(`{"id":%q,"title":%q,"owner":%q%s}`, id, strings.Repeat("Ti[red]tle ", 11)[:120], owner, extra))
	}
	file := `{"plan":"v0.22-` + strings.Repeat("x", 20) + `","goal":` + fmt.Sprintf("%q", strings.Repeat("goal [x] ", 23)[:200]) + `,"tasks":[` + strings.Join(specs, ",") + `]}`
	f, err := plan.Parse([]byte(file))
	if err != nil {
		panic(err)
	}
	note := strings.Repeat("n[o]te ", 40)[:200]
	board := map[string]bus.BoardEntry{
		tasks[1]: {Owner: "coder", State: "blocked", Request: &bus.RequestInfo{ID: "1-0", BlockedReason: hostileText}},
		tasks[2]: {Owner: "coder", State: "done", Updated: now.Unix()},
		tasks[3]: {Owner: "coder", State: "working", Updated: now.Add(-50 * time.Hour).Unix()},
	}
	facts := map[string]bus.PlanFact{
		tasks[2]: {PR: 99999, Note: note},
		tasks[6]: {Done: now.Add(-time.Hour).UnixMilli(), PR: 1, Note: note},
		tasks[7]: {Done: now.Add(-30 * time.Hour).UnixMilli()},
	}
	p := planState{loaded: true, file: f, rec: bus.PlanRecord{Commit: strings.Repeat("ab", 20), LoadedAt: now.Add(-8 * 24 * time.Hour).UnixMilli()},
		ledger: map[string][]bus.Verdict{tasks[2]: {{ID: "5-0", Subject: tasks[2], Author: "coder", Reviewer: "foureyes", Decision: "reject"}}},
		failed: map[string]bool{"missions": true}, partial: true}
	p.in = plan.Inputs{Board: board, Facts: facts, Verdicts: p.ledger, BoardOK: true, FactsOK: true, VerdictsOK: true, AsksOK: true,
		Asks: []bus.Ask{{ID: "9-0", From: "master", Task: tasks[0], State: "open"}}}
	p.rows = plan.Derive(f, p.in)
	return p
}

func TestHostilePlanHasEveryState(t *testing.T) {
	seen := map[plan.State]bool{}
	for _, r := range hostilePlan(time.Now()).rows {
		seen[r.State] = true
	}
	for _, s := range plan.Order {
		if !seen[s] {
			t.Errorf("the fixture lacks %s", s)
		}
	}
}

func recapTitles(m recapModel, section string) []string {
	var out []string
	for _, r := range m.Rows {
		if r.Section == section && r.Title != section {
			out = append(out, r.Title+" | "+r.Detail)
		}
	}
	return out
}

func TestRecapPlanSection(t *testing.T) {
	now := time.Now()
	since := now.Add(-2 * time.Hour)
	p := hostilePlan(now)
	// Facts newer than the last visit prove a change; older ones do not.
	for i := range p.rows {
		p.rows[i].LastFact = 0
	}
	p.rows[2].LastFact = now.Add(-time.Hour).UnixMilli()      // in review, changed since
	p.rows[3].LastFact = now.Add(-50 * time.Hour).UnixMilli() // in progress, before the visit
	m := buildRecap(recapInput{Now: now, Since: since, Plan: &p, Errors: map[string]error{}})
	planRows := strings.Join(recapTitles(m, "Plan"), "\n")
	if !strings.Contains(planRows, p.rows[2].Task.ID) || strings.Contains(planRows, p.rows[3].Task.ID) {
		t.Fatalf("only a change proven by a fact after the visit is listed:\n%s", planRows)
	}
	if !strings.Contains(planRows, "2/8?") {
		t.Fatalf("the plan line with its count and the kept-value mark:\n%s", planRows)
	}
	expected := strings.Join(recapTitles(m, "Expected from you"), "\n")
	if !strings.Contains(expected, p.rows[0].Task.ID) {
		t.Fatalf("a plan task waiting for the person is expected from them:\n%s", expected)
	}

	// No previous visit: no transition is guessed.
	m = buildRecap(recapInput{Now: now, Plan: &p, Errors: map[string]error{}})
	if rows := recapTitles(m, "Plan"); len(rows) != 2 || !strings.Contains(rows[1], "no previous visit") {
		t.Fatalf("no visit: %v", rows)
	}
	// No plan: no section.
	m = buildRecap(recapInput{Now: now, Since: since, Errors: map[string]error{}})
	for _, r := range m.Rows {
		if r.Section == "Plan" {
			t.Fatal("a Plan section with no plan loaded")
		}
	}
}

func TestPlanOverlayEscapesOnce(t *testing.T) {
	p := hostilePlan(time.Now())
	st := newPlanShared()
	st.plan = p
	h := newHarness(t, st)
	h.draw(160, 50)
	h.key(runeKey('P'))
	scr := h.draw(160, 50)
	if !strings.Contains(scr, "Ti[red]tle") || strings.Contains(scr, "[red[]") || !strings.Contains(scr, "goal [x]") {
		t.Fatalf("hostile text is shown as written, escaped once:\n%s", scr)
	}
}

func TestPlanRowsFromKeptValuesAreMarked(t *testing.T) {
	f := newPlanFake(true)
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	_, plain, _ := planOverlay(st.plan, -1, true, time.Now(), 120)
	if strings.Contains(plain, " ?\n") {
		t.Fatalf("a fresh read marks nothing:\n%s", plain)
	}
	st.plan.failed["missions"] = true // the missions read failed, values kept
	_, plain, _ = planOverlay(st.plan, -1, true, time.Now(), 120)
	for _, l := range strings.Split(plain, "\n") {
		if strings.Contains(l, "deploy") != strings.HasSuffix(l, " ?") {
			t.Errorf("only the mission task is marked: %q", l)
		}
	}
}

func TestPlanVerdictsUnknownUntilTheLedgerCaughtUp(t *testing.T) {
	f := newPlanFake(true)
	for i := 0; i < planLedgerPage*planLedgerPages+10; i++ { // more than one tick reads
		f.ledger = append(f.ledger, bus.Verdict{ID: fmt.Sprintf("%d-0", i+1), Subject: "other", Author: "a", Reviewer: "b", Decision: "approve"})
	}
	f.ledger = append(f.ledger, bus.Verdict{ID: "999999-0", Subject: "t", Author: "coder", Reviewer: "foureyes", Decision: "approve"})
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	for _, r := range st.plan.rows {
		if r.Task.ID == "roles" && r.State != plan.Unknown {
			t.Fatalf("mid catch-up the review is not known yet: %+v", r)
		}
	}
	pollOnce(context.Background(), f, st, 6)
	for _, r := range st.plan.rows {
		if r.Task.ID == "roles" && r.State != plan.Waiting {
			t.Fatalf("caught up: approved, waiting for the person: %+v", r)
		}
	}
}
