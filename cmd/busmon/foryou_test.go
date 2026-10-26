package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestForYouHint(t *testing.T) {
	cases := []struct {
		state, msg, person string
		want               bool
	}{
		{"idle", "lot 1 delivered; awaiting bernard", "bernard", true},
		{"idle", "Lot 1 delivered. Awaiting Bernard.", "bernard", true},
		{"idle", "over to bernard: review PR 12", "bernard", true},
		{"idle", "waiting for bernardo", "bernard", false},
		{"idle", "see https://example.com/bernard/x", "bernard", false},
		{"idle", "notes in /home/bernard/notes.md", "bernard", false},
		{"idle", "nothing to do", "bernard", false},
		{"waiting", "awaiting bernard", "bernard", false}, // waiting says it already
		{"working", "awaiting bernard", "bernard", false},
		{"idle", "piloted by master; awaiting pilot", "pilot", false}, // the default identity is a bus word
		{"idle", "awaiting bernard", "", false},
	}
	for _, c := range cases {
		if got := forYouHint(c.state, c.msg, c.person); got != c.want {
			t.Errorf("forYouHint(%q, %q, %q) = %v, want %v", c.state, c.msg, c.person, got, c.want)
		}
	}
}

func TestForYouFlagOnlyHints(t *testing.T) {
	a := &agentState{state: "idle", message: "awaiting bernard", forYou: true}
	fs := agentFlags(a, false)
	if len(fs) == 0 || fs[0].Word != "for you" || fs[0].Role != RoleAttention {
		t.Fatalf("flags %+v", fs)
	}
	// A hint never makes the agent wait for the person: no bell, no count.
	if waitsForPerson(a) {
		t.Fatal("the hint counts as a wait")
	}
	if _, word, _, _ := stateLook(a); word != "idle" {
		t.Fatalf("declared state relabelled %q", word)
	}
}

func TestPollAndTailSetTheHint(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"master": {State: "idle", Message: "lot 1 delivered; awaiting bernard", TS: 1},
		"coder":  {State: "idle", Message: "done", TS: 1}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{},
		health: health{ok: true}, person: "bernard"}
	pollOnce(context.Background(), f, st, 1)
	if !st.agents["master"].forYou || st.agents["coder"].forYou {
		t.Fatalf("poll: master %v coder %v", st.agents["master"].forYou, st.agents["coder"].forYou)
	}

	h := newHarness(t, st)
	h.u.ingest(bus.Event{ID: "2-0", Kind: "status", Agent: "master", State: "working", Message: "on it"})
	h.u.ingest(bus.Event{ID: "3-0", Kind: "status", Agent: "coder", State: "idle", Message: "handed to Bernard"})
	if st.agents["master"].forYou || !st.agents["coder"].forYou {
		t.Fatalf("tail: master %v coder %v", st.agents["master"].forYou, st.agents["coder"].forYou)
	}
}

func TestForYouDetailLine(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "idle", message: "awaiting bernard", stateAt: now, forYou: true}
	_, body := agentDetail("master", a, false, nil, nil, now)
	if !strings.Contains(plainOf(body), "It names you but declared idle: no question is open.") {
		t.Fatalf("detail:\n%s", plainOf(body))
	}
	a.forYou = false
	if _, body := agentDetail("master", a, false, nil, nil, now); strings.Contains(plainOf(body), "It names you") {
		t.Fatal("detail line without the hint")
	}
}
