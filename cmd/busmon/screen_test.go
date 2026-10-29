package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A filter hides; it never forgets. Clearing it brings the whole journal back.
func TestFilterHidesWithoutForgetting(t *testing.T) {
	lines := []feedLine{
		{id: "1", ev: bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "helper", Target: "coder", Message: "rebase"}},
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

// The shared snapshot is written by the poller and the tail while the shell
// takes its copy (takeSnapshot, under the lock) and draws it; -race turns any
// slip here into a failure.
func TestSharedStateIsSafeUnderConcurrentAccess(t *testing.T) {
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	m := shellOf(t, st, 120, 30, nil)
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
		m = step(t, m, pollMsg{snap: takeSnapshot(st, time.Now()), at: time.Now()})
		_ = m.View()
	}
	wg.Wait()
}
