package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// feedShell is the shell with n Activity lines and the compose window open.
func feedShell(t *testing.T, n int) Model {
	t.Helper()
	m := fixtureShell(t, 140, 40)
	var evs []bus.Event
	for i := 0; i < n; i++ {
		evs = append(evs, bus.Event{Kind: "report", Agent: "coder", ID: fmt.Sprintf("%d-%d", shellNow.Add(-time.Hour).UnixMilli()+int64(i), i),
			Message: "a report line with some length to it"})
	}
	m = step(t, m, tailMsg{events: evs, backfill: true})
	m.openMessageCompose("", "coder")
	return typeText(t, m, "some text")
}

// The person's lag: every message laid out every Activity line again (30-40
// ms with a full feed). A message that changes no line lays out none; a new
// line lays out only itself (and a day separator, when one opens).
func TestActivityRowsAreLaidOutOncePerChange(t *testing.T) {
	m := feedShell(t, 1000)
	activityRowCalls = 0
	m = keys(t, m, kBackspce, kBackspce, kBackspce)
	m = step(t, m, fadeMsg(shellNow.Add(time.Second)))
	if activityRowCalls != 0 {
		t.Fatalf("keys and a fade frame laid out %d Activity rows, want 0", activityRowCalls)
	}
	m = step(t, m, tailMsg{events: []bus.Event{{Kind: "report", Agent: "coder", ID: fmt.Sprintf("%d-0", shellNow.UnixMilli()), Message: "new"}}})
	if activityRowCalls == 0 || activityRowCalls > 2 {
		t.Fatalf("one new line laid out %d rows, want itself (and at most a separator)", activityRowCalls)
	}
}

// A cached row is never stale: each input the row reads lays it out again.
func TestActivityRowCacheFollowsEveryInput(t *testing.T) {
	var c activityCache
	fl := feedLine{id: "1-0", ev: bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "coder", ID: "1-0", Message: "hello"}, at: shellNow}
	base := c.row(fl, TierWide, 100, shellNow).text()
	check := func(name string, got Line) {
		t.Helper()
		if got.text() == base {
			t.Errorf("%s: the cached row was served unchanged", name)
		}
	}
	marked := fl
	marked.mark = markUnread
	check("mark", c.row(marked, TierWide, 100, shellNow))
	check("width", c.row(fl, TierWide, 80, shellNow))
	check("tier", c.row(fl, TierNarrow, 100, shellNow))
	fan := fl
	fan.to = []string{"coder", "foureyes"}
	check("fan-out", c.row(fan, TierWide, 100, shellNow))
	edited := fl
	edited.ev.Message = "changed"
	check("event", c.row(edited, TierWide, 100, shellNow))
	sep := feedLine{id: "sep", sep: true, at: shellNow}
	today := c.row(sep, TierWide, 100, shellNow).text()
	if c.row(sep, TierWide, 100, shellNow.Add(48*time.Hour)).text() == today || !strings.Contains(today, "(today)") {
		t.Error("a day separator kept '(today)' on another day")
	}
	// The same inputs give the row laid out once.
	activityRowCalls = 0
	c.row(fl, TierWide, 100, shellNow)
	c.row(fl, TierWide, 100, shellNow)
	if activityRowCalls > 1 {
		t.Errorf("the same row laid out %d times", activityRowCalls)
	}
}

var _ = tea.KeyPressMsg{}

// An ask's row also reads the ask's current state (title, open questions):
// an unchanged ask event across changed snapshots is laid out again
// (reviewer-codex).
func TestActivityRowCacheFollowsTheAsk(t *testing.T) {
	saved := currentAsks
	defer func() { currentAsks = saved }()
	var c activityCache
	q := func(id string) bus.AskQuestionState {
		return bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: id, Question: id, Options: []bus.AskOption{{Label: "yes"}}}}
	}
	ev := bus.Event{Kind: "cmd", Type: bus.CmdAsk, From: "master", Target: "people", ID: "5-0", Message: "review?"}
	fl := feedLine{id: "5-0", ev: ev, at: shellNow}
	currentAsks = map[string]bus.Ask{"5-0": {ID: "5-0", Title: "review lot 1?", State: "open", Questions: []bus.AskQuestionState{q("a"), q("b")}}}
	open2 := c.row(fl, TierWide, 120, shellNow).text()
	answered := q("a")
	answered.Answer = &bus.AskAnswer{Choice: "yes"}
	currentAsks = map[string]bus.Ask{"5-0": {ID: "5-0", Title: "review lot 1?", State: "open", Questions: []bus.AskQuestionState{answered, q("b")}}}
	open1 := c.row(fl, TierWide, 120, shellNow).text()
	if open1 == open2 || !strings.Contains(open1, "1 open") {
		t.Fatalf("an answered question kept the row: %q then %q", open2, open1)
	}
	currentAsks = map[string]bus.Ask{}
	if gone := c.row(fl, TierWide, 120, shellNow).text(); gone == open1 {
		t.Fatalf("an ask gone from the snapshot kept its row: %q", gone)
	}
}
