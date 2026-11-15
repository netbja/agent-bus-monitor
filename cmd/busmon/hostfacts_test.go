package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

type eventsStub struct {
	evs []bus.TermEvent // newest first, as RecentEvents
	err error
}

func (e eventsStub) RecentEvents(context.Context, int64) ([]bus.TermEvent, error) {
	return e.evs, e.err
}

var hostNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func ev(kind, agent, detail string, ago time.Duration) bus.TermEvent {
	return bus.TermEvent{Kind: kind, Agent: agent, Detail: detail, At: hostNow.Add(-ago)}
}

// An agent is blocked when its latest blocked/unblocked event is a blocked
// one; a background skip shows for a while; another agent's events are its own.
func TestHostFactsFromEvents(t *testing.T) {
	evs := eventsStub{evs: []bus.TermEvent{ // newest first
		ev(bus.EventBlocked, "coder", "Allow Bash: rm -rf build?", time.Minute),
		ev(bus.EventUnblocked, "coder", "", 5*time.Minute),
		ev(bus.EventBlocked, "coder", "older prompt", 6*time.Minute),
		ev(bus.EventUnblocked, "foureyes", "", 2*time.Minute),
		ev(bus.EventBlocked, "foureyes", "a prompt", 3*time.Minute),
		ev(bus.EventBackgroundSkip, "master", "auto-clear skipped — 2 checks running", 3*time.Minute),
		ev(bus.EventBackgroundSkip, "master", "pop-retire skipped — an older one", 4*time.Minute),
		ev(bus.EventBackgroundSkip, "architect", "pop-retire skipped — old", hostSkipShown+time.Minute),
	}}
	f, err := readHostFacts(context.Background(), evs, nil, "p", hostNow)
	if err != nil {
		t.Fatal(err)
	}
	if c := f["coder"]; !c.blocked || c.blockedWhy != "Allow Bash: rm -rf build?" || !c.blockedAt.Equal(hostNow.Add(-time.Minute)) {
		t.Fatalf("coder %+v", c)
	}
	if f["foureyes"].blocked {
		t.Fatal("an unblocked agent reads blocked")
	}
	if m := f["master"]; m.skip != "auto-clear skipped — 2 checks running" || m.blocked {
		t.Fatalf("master %+v", m)
	}
	if a := f["architect"]; a.skip != "" {
		t.Fatalf("an old skip still shows: %+v", a)
	}
}

// A draft is read from the agent's newest live terminal on a holder.
func TestHostFactsDraftFromTheTerminal(t *testing.T) {
	h := fake.New()
	start := func(name string) termhost.Term {
		tm, err := h.Start(context.Background(), termhost.Spec{Project: "p", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cur := start("coder")
	h.SetActivity(cur.ID, termhost.Activity{Draft: true, DraftSince: hostNow.Add(-2 * time.Minute)})
	closed := start("coder") // newer, but its program ended
	h.SetActivity(closed.ID, termhost.Activity{Draft: true, DraftSince: hostNow.Add(-time.Hour)})
	if err := h.Close(context.Background(), closed.ID); err != nil {
		t.Fatal(err)
	}
	older := start("foureyes") // a live terminal older than the agent's newest
	h.SetActivity(older.ID, termhost.Activity{Draft: true, DraftSince: hostNow.Add(-time.Hour)})
	clean := start("foureyes")
	h.SetActivity(clean.ID, termhost.Activity{})
	f, err := readHostFacts(context.Background(), eventsStub{}, h, "p", hostNow)
	if err != nil {
		t.Fatal(err)
	}
	if c := f["coder"]; !c.draft || !c.draftSince.Equal(hostNow.Add(-2*time.Minute)) {
		t.Fatalf("coder %+v", c)
	}
	if f["foureyes"].draft {
		t.Fatal("the newest terminal has no draft; an older one's shows")
	}
}

func TestHostFactsReadFailure(t *testing.T) {
	if _, err := readHostFacts(context.Background(), eventsStub{err: errors.New("redis down")}, nil, "p", hostNow); err == nil {
		t.Fatal("a failed events read is not reported")
	}
}

// The row says blocked, draft and the skip; the page says what each is.
func TestHostFactsOnTheRowAndPage(t *testing.T) {
	a := &agentState{state: "working", stateAt: hostNow, host: hostFacts{blocked: true, blockedWhy: "Allow Bash?", blockedAt: hostNow.Add(-time.Minute),
		draft: true, draftSince: hostNow.Add(-3 * time.Minute), skip: "auto-clear skipped — 2 checks running", skipAt: hostNow.Add(-time.Minute)}}
	var words []string
	for _, f := range agentFlags(a, false) {
		words = append(words, f.Word)
	}
	for _, w := range []string{"blocked", "draft", "bg work"} {
		if !strings.Contains(strings.Join(words, ","), w) {
			t.Fatalf("flags %v lack %q", words, w)
		}
	}
	_, body := agentDetail("coder", a, false, nil, nil, hostNow)
	for _, want := range []string{"Blocked on", "Allow Bash?", "Draft", "unsent", "Background", "auto-clear skipped"} {
		if !strings.Contains(plainOf(body), want) {
			t.Fatalf("detail lacks %q:\n%s", want, plainOf(body))
		}
	}
	plain := &agentState{state: "idle", stateAt: hostNow}
	for _, f := range agentFlags(plain, false) {
		if f.Word == "blocked" || f.Word == "draft" || f.Word == "bg work" {
			t.Fatalf("an agent without host facts flags %q", f.Word)
		}
	}
	if g := agentGroup(a, false); g != 1 {
		t.Fatalf("a blocked agent sorts in group %d, want 1", g)
	}
}

// The watcher writes the facts on the agents busmon knows, and clears them
// when they are gone.
func TestApplyHostFacts(t *testing.T) {
	st := &shared{agents: map[string]*agentState{"coder": {host: hostFacts{draft: true}}, "master": {}}}
	applyHostFacts(st, map[string]hostFacts{"master": {blocked: true}, "ghost": {blocked: true}})
	if st.agents["coder"].host.draft || !st.agents["master"].host.blocked || st.agents["ghost"] != nil {
		t.Fatalf("coder %+v master %+v", st.agents["coder"].host, st.agents["master"].host)
	}
}
