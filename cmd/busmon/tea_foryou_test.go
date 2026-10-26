package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// forYouFixture: two asks (one older), a waiting agent with an open ask (it
// counts once, as its ask), two waiting agents without one, an observed
// prompt, an answered wait, an idle agent with the hint, two gates and two
// unread lines plus a superseded one.
func forYouFixture() (snapshot, []feedLine) {
	t0 := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	ms := func(m int) int64 { return t0.Add(time.Duration(m) * time.Minute).UnixMilli() }
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	q := func(id string, answered bool) bus.AskQuestionState {
		s := bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: id, Header: id, Question: "question " + id}}
		if answered {
			s.Answer = &bus.AskAnswer{Choice: "a"}
		}
		return s
	}
	s := snapshot{
		person: "bernard",
		asks: []bus.Ask{
			{ID: "200-0", From: "master", Title: "review lot 1?", State: "open", CreatedAt: ms(20), Questions: []bus.AskQuestionState{q("q1", false), q("q2", false), q("q3", true)}},
			{ID: "100-0", From: "architect", Title: "pick a store", State: "open", CreatedAt: ms(10), Questions: []bus.AskQuestionState{q("q1", false)}},
			{ID: "50-0", From: "coder", Title: "closed one", State: "closed", CreatedAt: ms(5), Questions: []bus.AskQuestionState{q("q1", true)}},
		},
		agents: map[string]*agentState{
			"master":    {state: "waiting", message: "see my ask", stateAt: at(19), openAsk: 2, askID: "200-0"},
			"coder":     {state: "waiting", message: "need a decision", stateAt: at(30)},
			"sentinel":  {state: "waiting", message: "older wait", stateAt: at(25)},
			"ui-grok":   {state: "idle", obsPrompt: true, obsAt: at(40), obsSince: at(35), judgedAt: at(41)},
			"ui-kimi":   {state: "waiting", message: "answered", stateAt: at(1), answeredAt: at(2)},
			"architect": {state: "idle", message: "bernard: a hint", forYou: true, openAsk: 1, askID: "100-0"},
			"foureyes": {state: "working", gated: 2, gates: map[string]string{
				"1792000000000-0": "coder|review launcher-resume", "pr-9": "master|look at PR 9"}},
		},
	}
	ev := func(id, from, msg string) bus.Event {
		return bus.Event{ID: id, Kind: "cmd", Type: bus.CmdDirective, From: from, Target: "bernard", Message: msg}
	}
	feed := []feedLine{
		{id: "1", ev: ev("1792999000000-0", "coder", "old, superseded"), at: at(45), mark: markSuperseded},
		{id: "2", ev: ev("1792999100000-0", "coder", "Lot 1 delivered"), at: at(50), mark: markUnread},
		{id: "3", ev: ev("1792999050000-0", "master", "about the plan"), at: at(48), mark: markUnread},
		{id: "4", ev: ev("1792999200000-0", "master", "not for you"), at: at(55)},
		{id: "5", sep: true, at: at(56), mark: markUnread},
	}
	return s, feed
}

func TestForYouRanksAndOrders(t *testing.T) {
	s, feed := forYouFixture()
	got := forYou(s, feed)
	want := []struct {
		rank  int
		agent string
		key   string
	}{
		{rankAsk, "architect", "ask:100-0"},
		{rankAsk, "master", "ask:200-0"},
		{rankWait, "sentinel", "wait:sentinel"},
		{rankWait, "coder", "wait:coder"},
		{rankWait, "ui-grok", "prompt:ui-grok"},
		{rankGate, "foureyes", "gate:foureyes:1792000000000-0"}, // a dated ref first
		{rankGate, "foureyes", "gate:foureyes:pr-9"},
		{rankUnread, "master", "unread:1792999050000-0"},
		{rankUnread, "coder", "unread:1792999100000-0"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Rank != w.rank || g.Agent != w.agent || g.Key[:len(w.key)] != w.key {
			t.Errorf("item %d = rank %d %s %s, want rank %d %s %s", i, g.Rank, g.Agent, g.Key, w.rank, w.agent, w.key)
		}
	}
	if got[1].Count != 2 || got[1].Text != "review lot 1?" {
		t.Errorf("ask row: %+v", got[1])
	}
	if got[6].Text != "look at PR 9" {
		t.Errorf("gate row text: %q", got[6].Text)
	}
}

// Every surface is a named subset of the one list.
func TestForYouSubsets(t *testing.T) {
	s, feed := forYouFixture()
	items := forYou(s, feed)
	if n := len(decisions(items)); n != 7 {
		t.Errorf("for-you badge (decisions) = %d, want 7", n)
	}
	for _, it := range decisions(items) {
		if it.Rank == rankUnread {
			t.Errorf("a decision is an unread line: %+v", it)
		}
	}
	if n := unreadItems(items); n != 2 {
		t.Errorf("unread badge = %d, want 2", n)
	}
	// The reminder's source is decisions: never an unread line.
	rem := dueAttention(decisions(items), &busmonCache{Snoozes: map[string]int64{}}, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), 5*time.Minute)
	for _, it := range rem {
		if it.Rank == rankUnread {
			t.Errorf("the reminder lists an unread line: %+v", it)
		}
	}
	if len(rem) == 0 {
		t.Error("the reminder lists nothing")
	}
}

// An item leaves the list when its fact goes.
func TestForYouItemsLeaveWithTheirFact(t *testing.T) {
	s, feed := forYouFixture()
	s.asks[0].State = "closed"
	s.agents["coder"].state = "working"
	s.agents["foureyes"].gates, s.agents["foureyes"].gated = nil, 0
	feed[1].mark = markNone
	for _, it := range forYou(s, feed) {
		switch it.Key {
		case "ask:200-0", "unread:1792999100000-0":
			t.Errorf("still listed: %s", it.Key)
		}
		if it.Agent == "coder" && it.Rank == rankWait || it.Rank == rankGate {
			t.Errorf("still listed: %+v", it)
		}
	}
}
