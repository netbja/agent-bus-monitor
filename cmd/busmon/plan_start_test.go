package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// busmon-detail-labels (Charlie): a plan task waiting for the person had no
// way to act from its page. busmon-plan-start-ux (Charlie): the action is a
// row of the fields block, right under Owner ("Action:  enter  start the
// discussion with master"), not a line floating below it; Enter or s start it,
// the key cell drawn from the overlay key table. Starting opens the compose
// window to master, prefilled; nothing is sent until the window's own send key,
// nothing written to the plan.
func TestPlanTaskStartAction(t *testing.T) {
	f := newPlanFake(true)
	done := time.Now().Add(-time.Hour).UnixMilli()
	f.facts = map[string]bus.PlanFact{"roles": {Done: done}, "reload": {Done: done}} // release (the person's) is due
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	fw := &fakeWriter{}
	m := shellOf(t, st, 140, 40, fw)
	keysOf := tourKeys(tourRef{m: ModeOverlay, act: ActPlanStart})
	if keysOf != "enter s" {
		t.Fatalf("the overlay table starts a plan task with %q, want \"enter s\"", keysOf)
	}
	open := func(task string) Model {
		n := m
		n.rd = newReader()
		run(t, n, n.push(page{kind: ovPlanTask, key: task}))
		return n
	}
	w := open("release")
	lines := strings.Split(plainOf(w.rd.top().body), "\n")
	owner := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "Owner:") {
			owner = i
		}
	}
	if owner < 0 || owner+1 >= len(lines) {
		t.Fatalf("no Owner row:\n%s", strings.Join(lines, "\n"))
	}
	action := lines[owner+1]
	if !strings.HasPrefix(action, "Action:") || !strings.Contains(action, "enter  start the discussion with master") {
		t.Fatalf("the row under Owner is %q, want the action row", action)
	}
	if col := func(l string) int { return len(l) - len(strings.TrimLeft(l[strings.Index(l, ":")+1:], " ")) }; col(action) != col(lines[owner]) {
		t.Errorf("the action's value is not in the block's value column:\n%s\n%s", lines[owner], action)
	}
	for _, l := range w.rd.top().body {
		for _, sg := range l {
			if sg.Text == "enter" && sg.Role != RoleAccent {
				t.Errorf("the key cell is %v, want the accent", sg.Role)
			}
		}
	}
	if body := plainOf(open("deploy").rd.top().body); strings.Contains(body, "Action:") {
		t.Fatalf("a task in progress offers the action:\n%s", body)
	}
	// The footer advertises it on that page only.
	if f := footerOf(w); !strings.Contains(f, "enter s start") {
		t.Errorf("the startable page's footer lacks the key: %q", f)
	}
	if f := footerOf(open("deploy")); strings.Contains(f, " start") {
		t.Errorf("another plan task's footer offers the start: %q", f)
	}
	// c no longer starts it; Enter and s on another task do nothing.
	if n := keys(t, open("release"), 'c'); n.ui.compose != nil {
		t.Fatal("c still starts a plan task")
	}
	for _, k := range []any{kEnter, 's'} {
		if n := keys(t, open("deploy"), k); n.ui.compose != nil {
			t.Fatalf("%v opened the compose window on a task not waiting for the person", k)
		}
	}
	for _, k := range []any{kEnter, 's'} {
		w := keys(t, open("release"), k)
		c := w.ui.compose
		if c == nil || c.kind != composeMessage {
			t.Fatalf("%v opened no compose window", k)
		}
		if got := c.to.value(); got != "@master" {
			t.Errorf("recipient %q, want @master", got)
		}
		if got, want := c.area.value(), "Plan release: I start this one - Tag and install"; got != want {
			t.Errorf("prefill %q, want %q", got, want)
		}
		if len(fw.sent) != 0 {
			t.Fatalf("sent without the send key: %q", fw.sent)
		}
		w = keys(t, w, kEsc)
		if w.ui.compose != nil || len(fw.sent) != 0 {
			t.Fatalf("Esc: compose %v, sent %q", w.ui.compose != nil, fw.sent)
		}
	}
	// An unsent draft is opened as it is, never overwritten by the prefill.
	d := open("release")
	d.ui.msgCompose.body = "my own draft"
	d = keys(t, d, kEnter)
	if d.ui.compose == nil || d.ui.compose.area.value() != "my own draft" || len(fw.sent) != 0 {
		t.Fatalf("with a draft: compose %v", d.ui.compose != nil)
	}
	// Outside a plan task page the keys start nothing, even on a page whose
	// key is a startable task's id.
	for _, pg := range []page{{kind: ovAgent, key: "release"}, {kind: ovAgent, key: "coder"}, {kind: ovHelp}, {kind: ovPlan}} {
		n := m
		n.rd = newReader()
		run(t, n, n.push(pg))
		n = keys(t, n, 's')
		if n.ui.compose != nil {
			t.Errorf("s on page %v opened the compose window", pg.kind)
		}
		if strings.Contains(footerOf(n), " start") {
			t.Errorf("page %v advertises the start", pg.kind)
		}
	}
}

// Each of the two conditions alone offers nothing (foureyes' survivors of
// busmon-detail-labels): the person's task not yet waiting, and a waiting
// task that is an agent's.
func TestPlanStartableNeedsBothConditions(t *testing.T) {
	p := planState{rows: []plan.Row{
		{Task: plan.Task{ID: "mine-waiting", Owner: plan.Person}, State: plan.Waiting},
		{Task: plan.Task{ID: "mine-later", Owner: plan.Person}, State: plan.Upcoming},
		{Task: plan.Task{ID: "agents-waiting", Owner: "coder"}, State: plan.Waiting},
	}}
	for id, want := range map[string]bool{"mine-waiting": true, "mine-later": false, "agents-waiting": false, "absent": false} {
		if _, ok := planStartable(p, id); ok != want {
			t.Errorf("%s: startable %v, want %v", id, ok, want)
		}
		_, body, _ := planTaskDetail(p, id, time.Now())
		if got := strings.Contains(plainOf(body), "Action:"); got != want {
			t.Errorf("%s: action row %v, want %v", id, got, want)
		}
	}
}

// footerOf is the drawn frame's last row.
func footerOf(m Model) string {
	rows := strings.Split(strings.TrimRight(m.grid().plain(), "\n"), "\n")
	return rows[len(rows)-1]
}
