package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-detail-labels (Bernard): a plan task waiting for the person had no
// way to act from its page. Its page ends with an action line (the key from
// the key table); the key opens the compose window to master, prefilled;
// nothing is sent until the window's own send key, nothing written to the plan.
func TestPlanTaskStartAction(t *testing.T) {
	f := newPlanFake(true)
	done := time.Now().Add(-time.Hour).UnixMilli()
	f.facts = map[string]bus.PlanFact{"roles": {Done: done}, "reload": {Done: done}} // release (the person's) is due
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	fw := &fakeWriter{}
	m := shellOf(t, st, 140, 40, fw)
	key := tourKeys(tourRef{m: ModeOverlay, act: ActPlanStart})
	if key == "" {
		t.Fatal("no key for the plan start in the overlay table")
	}
	open := func(task string) Model {
		n := m
		n.rd = newReader()
		run(t, n, n.push(page{kind: ovPlanTask, key: task}))
		return n
	}
	w := open("release")
	if body := plainOf(w.rd.top().body); !strings.Contains(body, key+"  start: write to master about this task") {
		t.Fatalf("a waiting task's page lacks the action line:\n%s", body)
	}
	if body := plainOf(open("deploy").rd.top().body); strings.Contains(body, "start: write to master") {
		t.Fatalf("a task in progress offers the action:\n%s", body)
	}
	// The key on another task does nothing.
	if n := keys(t, open("deploy"), []rune(key)[0]); n.ui.compose != nil {
		t.Fatal("the key opened the compose window on a task not waiting for the person")
	}
	w = keys(t, w, []rune(key)[0])
	c := w.ui.compose
	if c == nil || c.kind != composeMessage {
		t.Fatal("the key opened no compose window")
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
	// An unsent draft is opened as it is, never overwritten by the prefill.
	d := open("release")
	d.ui.msgCompose.body = "my own draft"
	d = keys(t, d, []rune(key)[0])
	if d.ui.compose == nil || d.ui.compose.area.value() != "my own draft" || len(fw.sent) != 0 {
		t.Fatalf("with a draft: compose %v", d.ui.compose != nil)
	}
}
