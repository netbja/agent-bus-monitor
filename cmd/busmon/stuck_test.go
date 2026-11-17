package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// stuckAgent is an agent whose observed screen is unrecognised (state
// unknown) and has stood unchanged for stood, read just now.
func stuckAgent(stood time.Duration) *agentState {
	now := time.Now()
	return &agentState{state: "working", pid: 10, obsState: "unknown", obsAt: now, obsSince: now.Add(-stood),
		obsScreenSince: now.Add(-stood), obsOwnPID: true, stuckAfter: 3 * time.Minute, judgedAt: now}
}

// agent-screen-blocked §3: a screen unrecognised and unchanged for
// stuck_after reads stuck, a person's look (group 0), with its own words;
// not before, not when off, not for a declared blocked or waiting, an open
// ask, or another process's reading.
func TestStuckReadsAfterStuckAfter(t *testing.T) {
	a := stuckAgent(4 * time.Minute)
	if g, w, r, grp := stateLook(a); w != "stuck" || grp != 0 || r != RoleAttention || g != GlyphBlocked {
		t.Fatalf("stateLook = %q %q %v %d, want stuck, attention, group 0", g, w, r, grp)
	}
	if got := stuckWords(a); got != "stuck: its screen is not recognised and has not changed for 4m" {
		t.Fatalf("words %q", got)
	}
	if got := stuckWords(stuckAgent(2*time.Hour + 5*time.Minute)); got != "stuck: its screen is not recognised and has not changed for "+humanAge(2*time.Hour+5*time.Minute) {
		t.Fatalf("words after hours %q (busmon's own age format)", got)
	}
	if !waitsForPerson(a) {
		t.Fatal("a stuck agent is not the person's to look at")
	}
	for name, b := range map[string]*agentState{
		"before":           stuckAgent(2 * time.Minute),
		"off":              func() *agentState { x := stuckAgent(time.Hour); x.stuckAfter = 0; return x }(),
		"declared blocked": func() *agentState { x := stuckAgent(time.Hour); x.state = "blocked"; return x }(),
		"declared waiting": func() *agentState { x := stuckAgent(time.Hour); x.state = "waiting"; return x }(),
		"open ask":         func() *agentState { x := stuckAgent(time.Hour); x.openAsk = 1; return x }(),
		"another process":  func() *agentState { x := stuckAgent(time.Hour); x.obsOwnPID = false; return x }(),
		"recognised":       func() *agentState { x := stuckAgent(time.Hour); x.obsState = "idle"; return x }(),
		"no hash":          func() *agentState { x := stuckAgent(time.Hour); x.obsScreenSince = time.Time{}; return x }(),
		"old reading":      func() *agentState { x := stuckAgent(time.Hour); x.obsAt = x.obsAt.Add(-time.Hour); return x }(),
		"stale reading": func() *agentState { // the dispatcher gone: its last reading ages out
			x := stuckAgent(time.Hour)
			x.judgedAt = x.obsAt.Add(10 * time.Minute)
			return x
		}(),
	} {
		if screenStuck(b) {
			t.Errorf("%s: reads stuck", name)
		}
	}
	// A prompt is blocked, never stuck: its observation is not unknown.
	p := stuckAgent(time.Hour)
	p.obsState, p.obsPrompt = "blocked", true
	if _, w, _, _ := stateLook(p); w != "blocked" {
		t.Fatalf("a prompt reads %q", w)
	}
}

// For you lists a stuck agent with its words, and its page heads the live
// screen with them.
func TestStuckInForYouAndOnThePage(t *testing.T) {
	a := stuckAgent(5 * time.Minute)
	found := ""
	for _, it := range forYou(snapshot{agents: map[string]*agentState{"coder": a}}, nil) {
		if it.Agent == "coder" {
			found = it.Text
		}
	}
	if !strings.HasPrefix(found, "stuck: its screen is not recognised") {
		t.Fatalf("For you: %q", found)
	}
	if attentionWords(a) != stuckWords(a) {
		t.Fatalf("the page's heading %q, want the stuck words", attentionWords(a))
	}
	got := plainOf(termPreviewLines(termView{found: true, lines: []string{"a", "weird screen"}, heading: attentionWords(a)}, 100))
	if !strings.Contains(got, "has not changed for 5m") || !strings.Contains(got, "weird screen") {
		t.Fatalf("page:\n%s", got)
	}
}

// Through the poll and the snapshot: the observation's screen_since and pid
// reach the row, the setting reaches every agent (busmon's own read of
// dispatch.stuck_after), and the state column reads stuck.
func TestStuckThroughThePollAndSettings(t *testing.T) {
	now := time.Now()
	f := &fakeReader{calls: map[string]int{},
		snaps:    map[string]bus.AgentSnapshot{"coder": {State: "working", TS: 1, Pane: "w2:p2", PID: 4242, PIDStart: 7}},
		observed: map[string]bus.Observation{"coder": {State: "unknown", At: now.UnixMilli(), Since: now.Add(-5 * time.Minute).UnixMilli(), PID: 4242, ScreenHash: "h1", ScreenSince: now.Add(-5 * time.Minute).UnixMilli()}}}
	settings := loadBusmonSettings(func(k string) string {
		if k == config.EnvName("dispatch.stuck_after") {
			return "180"
		}
		return ""
	}, settingsFlags{})
	if settings.StuckAfter != 3*time.Minute {
		t.Fatalf("settings stuck after %v", settings.StuckAfter)
	}
	st := newShared("charlie", settings) // as main builds it (foureyes: the wiring)
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 1)
	s := takeSnapshot(st, time.Now())
	if _, w, _, _ := stateLook(s.agents["coder"]); w != "stuck" {
		t.Fatalf("coder reads %q, want stuck", w)
	}
	// Another process's reading is not its own.
	f.observed["coder"] = bus.Observation{State: "unknown", At: now.UnixMilli(), PID: 999, ScreenHash: "h1", ScreenSince: now.Add(-5 * time.Minute).UnixMilli()}
	pollOnce(context.Background(), f, st, 1)
	if _, w, _, _ := stateLook(takeSnapshot(st, time.Now()).agents["coder"]); w == "stuck" {
		t.Fatal("another process's screen reads stuck")
	}
}
