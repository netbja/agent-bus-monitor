package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// eventsStub is a timeline (newest first, as RecentEvents) and a broker clock.
type eventsStub struct {
	evs   []bus.TermEvent
	err   error
	now   time.Time // the broker's clock (zero: hostNow)
	reads []int64   // the n of each RecentEvents
}

func (e *eventsStub) RecentEvents(_ context.Context, n int64) ([]bus.TermEvent, error) {
	e.reads = append(e.reads, n)
	if int64(len(e.evs)) > n {
		return e.evs[:n], e.err
	}
	return e.evs, e.err
}

func (e *eventsStub) ServerTime(context.Context) (time.Time, error) {
	if e.now.IsZero() {
		return hostNow, nil
	}
	return e.now, nil
}

// readOnce is a first read by a fresh watcher.
func readOnce(t *testing.T, evs eventsReader, terms activityReader) map[string]hostFacts {
	t.Helper()
	var w hostWatcher
	f, err := w.read(context.Background(), evs, terms, "p")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var evSeq int

// evAt is an event whose stream id follows its time (the broker's clock).
func evAt(kind, agent, detail string, at time.Time) bus.TermEvent {
	evSeq++
	return bus.TermEvent{ID: fmt.Sprintf("%d-%d", at.UnixMilli(), evSeq), Kind: kind, Agent: agent, Detail: detail, At: at}
}

var hostNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func ev(kind, agent, detail string, ago time.Duration) bus.TermEvent {
	return evAt(kind, agent, detail, hostNow.Add(-ago))
}

// An agent is blocked when its latest blocked/unblocked event is a blocked
// one; a background skip shows for a while; another agent's events are its own.
func TestHostFactsFromEvents(t *testing.T) {
	evs := &eventsStub{evs: []bus.TermEvent{ // newest first
		ev(bus.EventBlocked, "coder", "Allow Bash: rm -rf build?", time.Minute),
		ev(bus.EventUnblocked, "coder", "", 5*time.Minute),
		ev(bus.EventBlocked, "coder", "older prompt", 6*time.Minute),
		ev(bus.EventUnblocked, "foureyes", "", 2*time.Minute),
		ev(bus.EventBlocked, "foureyes", "a prompt", 3*time.Minute),
		ev(bus.EventBackgroundSkip, "master", "auto-clear skipped — 2 checks running", 3*time.Minute),
		ev(bus.EventBackgroundSkip, "master", "pop-retire skipped — an older one", 4*time.Minute),
		ev(bus.EventBackgroundSkip, "architect", "pop-retire skipped — old", hostSkipShown+time.Minute),
	}}
	f := readOnce(t, evs, nil)
	if len(evs.reads) != 1 || evs.reads[0] != hostEventsAll {
		t.Fatalf("the first read is not the whole stream: %v", evs.reads)
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
	f := readOnce(t, &eventsStub{}, h)
	if c := f["coder"]; !c.draft || !c.draftSince.Equal(hostNow.Add(-2*time.Minute)) {
		t.Fatalf("coder %+v", c)
	}
	if f["foureyes"].draft {
		t.Fatal("the newest terminal has no draft; an older one's shows")
	}
}

func TestHostFactsReadFailure(t *testing.T) {
	var w hostWatcher
	if _, err := w.read(context.Background(), &eventsStub{err: errors.New("redis down")}, nil, "p"); err == nil {
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

// filler is n unrelated events, newest first, the newest at t.
func filler(n int, t time.Time) []bus.TermEvent {
	out := make([]bus.TermEvent, n)
	for i := range out {
		out[i] = evAt(bus.EventWakeTyped, "master", "", t.Add(-time.Duration(i)*time.Millisecond))
	}
	return out
}

// An agent stays blocked however many other events follow, until its own
// unblocked event (reviewer-codex): the stream is a timeline read through a
// window, never the state itself.
func TestHostFactsBlockedOutlivesTheWindow(t *testing.T) {
	blocked := ev(bus.EventBlocked, "coder", "Allow Bash?", time.Hour)
	evs := &eventsStub{evs: append(filler(10, hostNow.Add(-50*time.Minute)), blocked)}
	var w hostWatcher
	ctx := context.Background()
	if f, err := w.read(ctx, evs, nil, "p"); err != nil || !f["coder"].blocked {
		t.Fatalf("first read: %+v %v", f["coder"], err)
	}
	// 600 more events: the next window holds none of coder's, and no gap
	// (it still reaches the previous newest).
	evs.evs = append(filler(hostEventsRead-1, hostNow.Add(-time.Minute)), evs.evs...)
	if f, err := w.read(ctx, evs, nil, "p"); err != nil || !f["coder"].blocked || f["coder"].blockedWhy != "Allow Bash?" {
		t.Fatalf("after the window moved: %+v %v", f["coder"], err)
	}
	if n := evs.reads[len(evs.reads)-1]; n != hostEventsRead {
		t.Fatalf("a window that reaches the last read is read again whole: %d", n)
	}
	// More than a window between two reads: the whole stream is read again,
	// and coder's unblocked event, behind the window, is seen.
	unblocked := ev(bus.EventUnblocked, "coder", "", 30*time.Second)
	evs.evs = append(append(filler(hostEventsRead+100, hostNow), unblocked), evs.evs...)
	f, err := w.read(ctx, evs, nil, "p")
	if err != nil || f["coder"].blocked {
		t.Fatalf("an unblocked event behind a full window: %+v %v (reads %v)", f["coder"], err, evs.reads[len(evs.reads)-2:])
	}
	if n := evs.reads[len(evs.reads)-1]; n != hostEventsAll {
		t.Fatalf("the gap was not read: %d", n)
	}
}

// A skip's age is judged on the broker's clock, the clock its time comes
// from, whatever busmon's own clock says (reviewer-codex).
func TestHostFactsSkipAgeOnTheBrokerClock(t *testing.T) {
	for _, skew := range []time.Duration{-3 * time.Hour, 0, 3 * time.Hour} {
		broker := time.Now().Add(skew)
		fresh := evAt(bus.EventBackgroundSkip, "master", "auto-clear skipped — fresh", broker.Add(-time.Minute))
		old := evAt(bus.EventBackgroundSkip, "coder", "pop-retire skipped — old", broker.Add(-hostSkipShown-time.Minute))
		f := readOnce(t, &eventsStub{evs: []bus.TermEvent{fresh, old}, now: broker}, nil)
		if f["master"].skip == "" || f["coder"].skip != "" {
			t.Fatalf("skew %v: master %q coder %q", skew, f["master"].skip, f["coder"].skip)
		}
	}
	// A retained skip ages out on a later read.
	evs := &eventsStub{evs: []bus.TermEvent{ev(bus.EventBackgroundSkip, "master", "auto-clear skipped", time.Minute)}}
	var w hostWatcher
	if f, _ := w.read(context.Background(), evs, nil, "p"); f["master"].skip == "" {
		t.Fatal("a fresh skip is hidden")
	}
	evs.now = hostNow.Add(hostSkipShown)
	if f, _ := w.read(context.Background(), evs, nil, "p"); f["master"].skip != "" {
		t.Fatal("a skip older than its window still shows")
	}
}
